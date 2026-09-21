package automation

import (
	"context"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ironystock/agentic-obs/internal/storage"
)

// Default retention policy for execution history. Operators can override
// at runtime via SetExecutionRetention / SetRetentionSweepInterval.
const (
	defaultExecutionRetention     = 30 * 24 * time.Hour // keep 30 days
	defaultRetentionSweepInterval = 1 * time.Hour       // sweep hourly
)

// AutomationEngine manages automation rules and their execution.
type AutomationEngine struct {
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	storage   *storage.DB
	executor  *Executor
	scheduler *ScheduleManager

	rules     map[int64]*Rule     // In-memory rule cache
	cooldowns map[int64]time.Time // Last execution time per rule

	eventChan chan EventPayload
	wg        sync.WaitGroup // Tracks in-flight processEvents + executeRule goroutines
	running   bool

	// droppedEvents counts events discarded because eventChan was full.
	// Accessed via sync/atomic so callers don't need to hold e.mu.
	droppedEvents atomic.Uint64

	// Retention sweep configuration. Guarded by e.mu.
	executionRetention     time.Duration
	retentionSweepInterval time.Duration

	// clock supplies the current time for cooldown decisions. Tests replace it
	// with a fake so they can advance past a deadline instead of sleeping
	// through it. (FB-56)
	clock clock

	// suppressor tells the engine's own writes apart from changes somebody else
	// made, so a rule that reacts to what it does cannot feed itself. (FB-85)
	suppressor *writeSuppressor

	// breaker is the backstop for the loops suppression cannot identify: it
	// disables a rule that keeps firing regardless of why. (FB-86)
	breaker *breaker

	// debounced holds the timer and the latest payload for each rule waiting
	// out its quiet period. Guarded by e.mu. (FB-87)
	debounced map[int64]*pendingRun
}

// pendingRun is a rule waiting for its debounce window to go quiet.
type pendingRun struct {
	timer   timer
	payload EventPayload
}

// NewAutomationEngine creates a new automation engine.
func NewAutomationEngine(db *storage.DB, obsClient OBSClient) *AutomationEngine {
	ctx, cancel := context.WithCancel(context.Background())

	engine := &AutomationEngine{
		ctx:                    ctx,
		cancel:                 cancel,
		storage:                db,
		executor:               NewExecutor(obsClient),
		suppressor:             newWriteSuppressor(realClock{}, suppressTTL),
		breaker:                newBreaker(realClock{}, oscillationThreshold, oscillationWindow),
		rules:                  make(map[int64]*Rule),
		cooldowns:              make(map[int64]time.Time),
		debounced:              make(map[int64]*pendingRun),
		eventChan:              make(chan EventPayload, 100),
		executionRetention:     defaultExecutionRetention,
		retentionSweepInterval: defaultRetentionSweepInterval,
		clock:                  realClock{},
	}

	// The executor records what it writes; the dispatcher reads those records.
	// They have to be the same suppressor or neither half does anything.
	engine.executor.useSuppressor(engine.suppressor)

	return engine
}

// setClock replaces the engine's source of time, including the suppressor's.
//
// Both have to move together. The suppressor expires entries on a deadline, so
// an engine running on a fake clock with a suppressor still on the real one has
// two notions of now -- which is the exact condition the clock seam exists to
// prevent, reintroduced one field at a time.
func (e *AutomationEngine) setClock(c clock) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.clock = c
	if e.suppressor != nil {
		e.suppressor.setClock(c)
	}
	if e.breaker != nil {
		e.breaker.setClock(c)
	}
}

// Start loads rules and begins processing events.
func (e *AutomationEngine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return nil
	}

	// Load enabled rules from storage
	if err := e.loadRulesLocked(); err != nil {
		return err
	}

	// Start scheduler
	e.scheduler = NewScheduleManager(e.executeScheduledRule)
	e.scheduler.Start()

	// Schedule all schedule-type rules
	for _, rule := range e.rules {
		if rule.Enabled && rule.TriggerType == TriggerTypeSchedule {
			if err := e.scheduler.Schedule(rule); err != nil {
				log.Printf("[Automation] Warning: failed to schedule rule '%s': %v", rule.Name, err)
			}
		}
	}

	// Start event processing goroutine
	e.wg.Add(1)
	go e.processEvents()

	// Start retention sweeper
	e.wg.Add(1)
	go e.retentionSweeper()

	e.running = true
	log.Printf("[Automation] Engine started with %d rules", len(e.rules))
	return nil
}

// SetExecutionRetention overrides how long execution history is kept
// before the background sweeper deletes it. Must be called before Start
// or the next sweep tick will use the new value.
func (e *AutomationEngine) SetExecutionRetention(d time.Duration) {
	e.mu.Lock()
	e.executionRetention = d
	e.mu.Unlock()
}

// SetRetentionSweepInterval overrides how often the retention sweeper
// runs. Must be called before Start; changes after Start do not
// reschedule the current ticker.
func (e *AutomationEngine) SetRetentionSweepInterval(d time.Duration) {
	e.mu.Lock()
	e.retentionSweepInterval = d
	e.mu.Unlock()
}

// retentionSweeper periodically purges old rule_executions records.
func (e *AutomationEngine) retentionSweeper() {
	defer e.wg.Done()

	e.mu.RLock()
	interval := e.retentionSweepInterval
	e.mu.RUnlock()
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if _, err := e.RunRetentionSweep(); err != nil && e.ctx.Err() == nil {
				log.Printf("[Automation] Retention sweep failed: %v", err)
			}
		}
	}
}

// RunRetentionSweep executes a single retention pass immediately and
// returns the number of rows removed. Exposed for tests and for
// operators who want to purge on demand.
func (e *AutomationEngine) RunRetentionSweep() (int64, error) {
	e.mu.RLock()
	retention := e.executionRetention
	e.mu.RUnlock()
	if retention <= 0 {
		return 0, nil
	}

	deleted, err := e.storage.ClearOldRuleExecutions(e.ctx, retention)
	if err != nil {
		return 0, err
	}
	if deleted > 0 {
		log.Printf("[Automation] Retention sweep: removed %d executions older than %s", deleted, retention)
	}
	return deleted, nil
}

// Stop gracefully shuts down the engine. Waits for all in-flight
// event dispatch and rule execution goroutines before returning so
// execution records never get stranded in the "running" state.
func (e *AutomationEngine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.running = false
	e.mu.Unlock()

	// Before anything else: a debounce timer that fires after this point would
	// execute a rule against an engine that has released its OBS client, and
	// would do it after the process believed it had finished.
	e.cancelDebounces()

	e.cancel()

	if e.scheduler != nil {
		e.scheduler.Stop()
	}

	close(e.eventChan)
	e.wg.Wait()
	log.Println("[Automation] Engine stopped")
}

// IsRunning returns whether the engine is running.
func (e *AutomationEngine) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.running
}

// HandleEvent dispatches an OBS event to matching rules.
func (e *AutomationEngine) HandleEvent(payload EventPayload) {
	if payload.Timestamp.IsZero() {
		payload.Timestamp = time.Now()
	}

	select {
	case e.eventChan <- payload:
	default:
		dropped := e.droppedEvents.Add(1)
		log.Printf("[Automation] Event buffer full, dropping event: %s (total dropped: %d)", payload.EventType, dropped)
	}
}

// DroppedEventsTotal returns the cumulative number of events discarded due
// to a full event buffer. Exposed for observability and tests.
func (e *AutomationEngine) DroppedEventsTotal() uint64 {
	return e.droppedEvents.Load()
}

// TriggerRule manually triggers a rule by ID.
func (e *AutomationEngine) TriggerRule(ruleID int64) error {
	e.mu.RLock()
	rule, exists := e.rules[ruleID]
	e.mu.RUnlock()

	if !exists {
		return &RuleNotFoundError{ID: ruleID}
	}

	e.wg.Add(1)
	go e.executeRule(rule, nil)
	return nil
}

// TriggerRuleByName manually triggers a rule by name.
func (e *AutomationEngine) TriggerRuleByName(name string) error {
	e.mu.RLock()
	var rule *Rule
	for _, r := range e.rules {
		if r.Name == name {
			rule = r
			break
		}
	}
	e.mu.RUnlock()

	if rule == nil {
		return &RuleNotFoundError{Name: name}
	}

	e.wg.Add(1)
	go e.executeRule(rule, nil)
	return nil
}

// ReloadRules reloads rules from storage.
func (e *AutomationEngine) ReloadRules() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.loadRulesLocked()
}

// GetRuleCount returns the number of loaded rules.
func (e *AutomationEngine) GetRuleCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.rules)
}

// loadRulesLocked loads enabled rules from storage (must hold lock).
func (e *AutomationEngine) loadRulesLocked() error {
	dbRules, err := e.storage.ListAutomationRules(e.ctx, true)
	if err != nil {
		return err
	}

	// Clear and rebuild
	e.rules = make(map[int64]*Rule)

	for _, dbRule := range dbRules {
		rule := convertStorageRule(dbRule)
		e.rules[rule.ID] = rule
	}

	log.Printf("[Automation] Loaded %d enabled rules", len(e.rules))
	return nil
}

// processEvents is the main event processing loop.
func (e *AutomationEngine) processEvents() {
	defer e.wg.Done()
	for payload := range e.eventChan {
		e.dispatchEvent(payload)
	}
}

// dispatchEvent finds and executes matching rules for an event.
//
// Cooldown is recorded at dispatch time (not at execute-end) so that a burst
// of events arriving faster than executeRule can complete cannot re-trigger
// the same rule. Because cooldown check + record must be atomic, the match
// loop runs under a write lock.
func (e *AutomationEngine) dispatchEvent(payload EventPayload) {
	// Our own echo, dropped before any rule sees it.
	//
	// This is ahead of the rule loop rather than inside it because the event is
	// not ours "for this rule" -- it is ours, full stop, and a second rule
	// watching the same event would otherwise pick up what the first one caused.
	// Cooldown cannot do this job: it is off by default, and when on it throttles
	// genuine events just as hard, because it counts rather than identifies.
	if key, keyed := keyForEvent(payload); keyed && e.suppressor.Consume(key) {
		log.Printf("[Automation] Ignoring %s: it is the echo of our own write",
			payload.EventType)
		return
	}

	e.mu.Lock()

	// Find matching rules, recording cooldown atomically for each match.
	var matching []*Rule
	now := e.clock.Now()
	for _, rule := range e.rules {
		if !rule.Enabled {
			continue
		}
		if rule.TriggerType != TriggerTypeEvent {
			continue
		}
		if rule.GetEventType() != payload.EventType {
			continue
		}
		if !e.matchesFilter(rule.GetEventFilter(), payload.Data) {
			continue
		}
		if !e.checkCooldownLocked(rule) {
			log.Printf("[Automation] Rule '%s' skipped (cooldown)", rule.Name)
			continue
		}
		if rule.CooldownMs > 0 {
			e.cooldowns[rule.ID] = now
		}
		matching = append(matching, rule)
	}

	e.mu.Unlock()

	// Sort by priority (higher first)
	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Priority > matching[j].Priority
	})

	// Execute matching rules
	for _, rule := range matching {
		if window := rule.GetDebounceMs(); window > 0 {
			e.deferRule(rule, payload, time.Duration(window)*time.Millisecond)
			continue
		}
		// Counted here rather than inside executeRule, because this is where the
		// decision to run is made. Counting after the fact would let a rule
		// spawn an unbounded number of goroutines before any of them reported.
		if tripped, count := e.breaker.Record(rule.ID); tripped {
			e.tripRule(rule, count)
			continue
		}
		e.wg.Add(1)
		go e.executeRule(rule, &payload)
	}
}

// deferRule restarts a rule's debounce window, keeping the latest payload.
//
// Trailing edge: the rule runs after the events stop, on the last one. That is
// the difference from cooldown, which runs on the *first* event and ignores the
// rest -- so cooldown acts on the state before the burst, and debounce acts on
// the state the operator ended up in. Showing nine layers in a scene fires a
// visibility rule nine times in a few milliseconds, and the rule wants to run
// once, at the end.
//
// The cooldown and breaker checks deliberately happen when the timer fires
// rather than here. Counting a scheduled run as a run would let a burst consume
// a rule's cooldown without it ever having executed.
func (e *AutomationEngine) deferRule(rule *Rule, payload EventPayload, window time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if existing, ok := e.debounced[rule.ID]; ok {
		existing.timer.Stop()
	}

	pending := &pendingRun{payload: payload}
	pending.timer = e.clock.AfterFunc(window, func() { e.runDebounced(rule.ID) })
	e.debounced[rule.ID] = pending
}

// runDebounced executes a rule whose window has gone quiet.
func (e *AutomationEngine) runDebounced(ruleID int64) {
	e.mu.Lock()

	pending, ok := e.debounced[ruleID]
	if !ok {
		e.mu.Unlock()
		return
	}
	delete(e.debounced, ruleID)

	rule, known := e.rules[ruleID]
	if !known || !rule.Enabled || !e.running {
		// Stopped, disabled or deleted while the window was running. A timer
		// that fired into a stopped engine would execute against a client the
		// engine has already let go of.
		e.mu.Unlock()
		return
	}

	// Cooldown is checked here, where the run actually happens.
	if !e.checkCooldownLocked(rule) {
		log.Printf("[Automation] Rule '%s' skipped after debounce (cooldown)", rule.Name)
		e.mu.Unlock()
		return
	}
	if rule.CooldownMs > 0 {
		e.cooldowns[rule.ID] = e.clock.Now()
	}
	e.mu.Unlock()

	if tripped, count := e.breaker.Record(rule.ID); tripped {
		e.tripRule(rule, count)
		return
	}

	payload := pending.payload
	e.wg.Add(1)
	go e.executeRule(rule, &payload)
}

// cancelDebounces drops every pending run. Called while stopping, so a timer
// cannot fire into an engine that has shut down.
func (e *AutomationEngine) cancelDebounces() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for id, pending := range e.debounced {
		pending.timer.Stop()
		delete(e.debounced, id)
	}
}

// oscillationError is the reason recorded against a rule the breaker disabled.
// It is a fixed string so an operator, or a tool, can search for it.
const oscillationError = "oscillation"

// tripRule disables a runaway rule and records why.
//
// Disabling is written to the database as well as to the in-memory cache: a
// rule that came back on the next restart, still oscillating, would be a worse
// bug than the one this exists to stop.
//
// The execution row matters as much as the disabling. A rule that switched
// itself off leaving no record is indistinguishable from one that never ran,
// and the operator's first move -- turning it back on -- would walk straight
// into the same loop.
func (e *AutomationEngine) tripRule(rule *Rule, count int) {
	// Stop the loop before doing anything that can fail.
	//
	// The order here is the whole fix. Writing to storage first looks tidier --
	// persist, then reflect it in memory -- and it does not work: a runaway rule
	// is already hammering the same SQLite file with its own execution rows, so
	// every write the breaker attempts comes back SQLITE_BUSY and the rule stays
	// enabled. The breaker was trying to record that it had tripped using the
	// resource the rule it was stopping had saturated. Measured: the disable and
	// the oscillation row both failed while the rule kept firing.
	//
	// Flipping the in-memory flag ends the storm in microseconds and needs
	// nothing that can be contended. Everything after this runs against a quiet
	// database.
	//
	// The direct cause of that failure was a missing busy_timeout, now set in
	// internal/storage, and with it the writes succeed in either order -- so the
	// tests pass whichever way round this is written. The order is kept because
	// a busy_timeout is a bounded wait: a loop fast enough to hold the file for
	// longer than the timeout would defeat it, and stopping first does not
	// depend on how fast the loop is. Defence in depth, stated rather than
	// implied, because nothing here proves it.
	e.mu.Lock()
	alreadyTripped := false
	if cached, ok := e.rules[rule.ID]; ok {
		alreadyTripped = !cached.Enabled
		cached.Enabled = false
	}
	rule.Enabled = false
	delete(e.cooldowns, rule.ID)
	e.mu.Unlock()

	if alreadyTripped {
		// Events queued before the flag flipped can arrive after it. Tripping
		// once per rule keeps one runaway from writing a hundred identical rows.
		return
	}

	log.Printf("[Automation] Rule '%s' (ID %d) fired %d times in %v and has been "+
		"disabled. Something it does is triggering it again; see ADR-010",
		rule.Name, rule.ID, count, oscillationWindow)

	now := e.clock.Now()
	completed := now
	if _, err := e.storage.CreateRuleExecution(e.ctx, storage.RuleExecution{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		TriggerType: rule.TriggerType,
		StartedAt:   now,
		CompletedAt: &completed,
		Status:      "failed",
		Error:       oscillationError,
	}); err != nil {
		log.Printf("[Automation] Could not record the oscillation of rule '%s': %v",
			rule.Name, err)
	}

	if err := e.storage.SetAutomationRuleEnabled(e.ctx, rule.ID, false); err != nil {
		// Worth saying loudly: the rule is off in memory but would be back on at
		// the next restart, still looping.
		log.Printf("[Automation] Could not disable runaway rule '%s' in the database: %v",
			rule.Name, err)
	}

	// A rule the operator turns back on starts with a clean history, or it
	// would trip on its first execution and look permanently broken.
	e.breaker.Forget(rule.ID)
}

// matchesFilter checks if event data matches the rule's event filter.
func (e *AutomationEngine) matchesFilter(filter map[string]interface{}, data map[string]interface{}) bool {
	if filter == nil || len(filter) == 0 {
		return true
	}

	for key, expected := range filter {
		actual, exists := data[key]
		if !exists {
			return false
		}
		// Simple equality check
		if actual != expected {
			return false
		}
	}

	return true
}

// checkCooldownLocked returns true if the rule can be executed (not in cooldown).
// Caller must hold e.mu (read or write).
func (e *AutomationEngine) checkCooldownLocked(rule *Rule) bool {
	if rule.CooldownMs <= 0 {
		return true
	}

	lastRun, exists := e.cooldowns[rule.ID]
	if !exists {
		return true
	}

	cooldown := time.Duration(rule.CooldownMs) * time.Millisecond
	return e.clock.Since(lastRun) >= cooldown
}

// executeScheduledRule is called by the scheduler.
func (e *AutomationEngine) executeScheduledRule(rule *Rule) {
	log.Printf("[Automation] Scheduled trigger for rule '%s'", rule.Name)
	e.wg.Add(1)
	e.executeRule(rule, nil)
}

// executeRule runs a single automation rule. Every call path into this
// method must precede it with e.wg.Add(1); executeRule matches with Done.
func (e *AutomationEngine) executeRule(rule *Rule, payload *EventPayload) {
	defer e.wg.Done()
	startTime := time.Now()
	log.Printf("[Automation] Executing rule '%s' (ID: %d)", rule.Name, rule.ID)

	// Create execution record
	exec := storage.RuleExecution{
		RuleID:      rule.ID,
		RuleName:    rule.Name,
		TriggerType: rule.TriggerType,
		StartedAt:   startTime,
		Status:      storage.ExecutionStatusRunning,
	}
	if payload != nil {
		exec.TriggerData = payload.Data
	}

	execID, err := e.storage.CreateRuleExecution(e.ctx, exec)
	if err != nil {
		log.Printf("[Automation] Warning: failed to create execution record: %v", err)
	}
	exec.ID = execID

	// Execute actions sequentially
	var actionResults []storage.ActionResult
	var execError error

	for i, action := range rule.Actions {
		result := e.executor.ExecuteAction(action, i)
		actionResults = append(actionResults, storage.ActionResult{
			ActionType: result.ActionType,
			Index:      result.Index,
			Success:    result.Success,
			Error:      result.Error,
			DurationMs: result.DurationMs,
		})

		if !result.Success && action.GetOnError() == ActionErrorStop {
			execError = &ActionError{
				ActionType: action.Type,
				Index:      i,
				Message:    result.Error,
			}
			break
		}
	}

	// Update execution record
	completedAt := time.Now()
	exec.CompletedAt = &completedAt
	exec.DurationMs = time.Since(startTime).Milliseconds()
	exec.ActionResults = actionResults

	if execError != nil {
		exec.Status = storage.ExecutionStatusFailed
		exec.Error = execError.Error()
		log.Printf("[Automation] Rule '%s' failed: %v", rule.Name, execError)
	} else {
		exec.Status = storage.ExecutionStatusCompleted
		log.Printf("[Automation] Rule '%s' completed in %dms", rule.Name, exec.DurationMs)
	}

	if execID > 0 {
		if err := e.storage.UpdateRuleExecution(e.ctx, exec); err != nil {
			log.Printf("[Automation] Warning: failed to update execution record: %v", err)
		}
	}

	// Update rule run stats
	if err := e.storage.UpdateRuleRunStats(e.ctx, rule.ID, startTime); err != nil {
		log.Printf("[Automation] Warning: failed to update rule stats: %v", err)
	}
}

// NotifyRuleChange should be called when rules are modified via MCP.
func (e *AutomationEngine) NotifyRuleChange(ruleID int64, deleted bool) {
	if deleted {
		e.mu.Lock()
		if rule, exists := e.rules[ruleID]; exists {
			if rule.TriggerType == TriggerTypeSchedule && e.scheduler != nil {
				e.scheduler.Unschedule(ruleID)
			}
			delete(e.rules, ruleID)
		}
		delete(e.cooldowns, ruleID)
		e.mu.Unlock()
		return
	}

	// Reload the specific rule
	dbRule, err := e.storage.GetAutomationRule(e.ctx, ruleID)
	if err != nil {
		log.Printf("[Automation] Warning: failed to reload rule %d: %v", ruleID, err)
		return
	}

	rule := convertStorageRule(dbRule)

	e.mu.Lock()
	defer e.mu.Unlock()

	// Update scheduler if needed
	if e.scheduler != nil {
		// Remove old schedule
		e.scheduler.Unschedule(ruleID)

		// Add new schedule if applicable
		if rule.Enabled && rule.TriggerType == TriggerTypeSchedule {
			if err := e.scheduler.Schedule(rule); err != nil {
				log.Printf("[Automation] Warning: failed to schedule rule '%s': %v", rule.Name, err)
			}
		}
	}

	if rule.Enabled {
		e.rules[ruleID] = rule
	} else {
		delete(e.rules, ruleID)
	}
}

// convertStorageRule converts a storage rule to an automation rule.
func convertStorageRule(dbRule *storage.AutomationRule) *Rule {
	actions := make([]Action, len(dbRule.Actions))
	for i, a := range dbRule.Actions {
		actions[i] = Action{
			Type:       a.Type,
			Parameters: a.Parameters,
			OnError:    a.OnError,
		}
	}

	return &Rule{
		ID:            dbRule.ID,
		Name:          dbRule.Name,
		Description:   dbRule.Description,
		Enabled:       dbRule.Enabled,
		TriggerType:   dbRule.TriggerType,
		TriggerConfig: dbRule.TriggerConfig,
		Actions:       actions,
		CooldownMs:    dbRule.CooldownMs,
		Priority:      dbRule.Priority,
		CreatedAt:     dbRule.CreatedAt,
		UpdatedAt:     dbRule.UpdatedAt,
		LastRun:       dbRule.LastRun,
		RunCount:      dbRule.RunCount,
	}
}

// Error types

// RuleNotFoundError is returned when a rule is not found.
type RuleNotFoundError struct {
	ID   int64
	Name string
}

func (e *RuleNotFoundError) Error() string {
	if e.Name != "" {
		return "automation rule '" + e.Name + "' not found"
	}
	return "automation rule not found"
}

// ActionError is returned when an action fails and stops execution.
type ActionError struct {
	ActionType string
	Index      int
	Message    string
}

func (e *ActionError) Error() string {
	return e.Message
}
