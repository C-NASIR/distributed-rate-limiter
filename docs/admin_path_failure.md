# Act 2: The Control Plane When Things Go Wrong

Let me tell you what happens when you're trying to update rules and disaster strikes at each step.

---

## The Scene Before the Failure

**10:00 AM, Normal Operations:**

Your operator (or automation) decides to update a rule for Tenant Acme:

```
Current: 100 requests/minute for /api/search
New:     500 requests/minute for /api/search
```

They send an API call to the admin endpoint:

```http
PUT /admin/rules
{
  "tenantID": "acme",
  "resource": "/api/search",
  "algorithm": "token_bucket",
  "limit": 500,
  "window": "60s",
  "expectedVersion": 7
}
```

**The request arrives at Instance #3** (behind the admin load balancer).

Let's trace it through the system and see what can go wrong at each step.

---

## Step 1: AdminHandler Receives the Request

```go
func (h *AdminHandlerV3) UpdateRule(ctx context.Context, req *UpdateRuleRequest) (*Rule, error) {
    // Step 1: Validate the request
    if req.Limit <= 0 {
        return nil, errors.New("invalid limit")
    }

    // Step 2: Call the database
    updatedRule, err := h.db.Update(ctx, ruleFromRequest(req), req.ExpectedVersion)
    if err != nil {
        return nil, err
    }

    // Step 3: Return the updated rule
    return updatedRule, nil
}
```

**This looks simple. But there's a lot that can fail.**

---

## Failure Scenario 1: PostgreSQL Primary Is Down

### **10:00:01 - The Database Call Fails**

```go
updatedRule, err := h.db.Update(ctx, rule, expectedVersion)
```

**Inside `RuleDB.Update()`:**

```go
func (db *PostgresRuleDB) Update(ctx context.Context, rule *Rule, expectedVersion int64) (*Rule, error) {
    // Start a transaction
    tx, err := db.primary.BeginTx(ctx, nil)
    if err != nil {
        // ❌ Connection failed!
        return nil, fmt.Errorf("failed to begin transaction: %w", err)
    }
    defer tx.Rollback()

    // ... (rest of the code never executes)
}
```

**What error do we get?**

```
Error: failed to begin transaction: dial tcp 10.0.8.45:5432: connect: connection refused
```

**What does the AdminHandler do?**

```go
updatedRule, err := h.db.Update(ctx, rule, expectedVersion)
if err != nil {
    // Just return the error to the client
    return nil, err
}
```

**The client gets an HTTP 500 response:**

```json
{
  "error": "failed to update rule: connection refused"
}
```

---

### **What Does This Mean for the System?**

**The good news:**

- ✅ **Nothing changed!** No partial writes, no corruption.
- ✅ The existing rule (version 7, limit 100) is still active everywhere
- ✅ Rate limiting continues to work normally (it only reads from replicas)

**The bad news:**

- ❌ The operator's update **failed completely**
- ❌ They need to retry

---

### **Does the System Go Offline?**

**No!** Let me be very explicit:

**The admin API is down, but the rate limiting API is still working perfectly.**

Here's why:

1. **Rate limiting (the hot path)** only needs:

   - ✅ RuleCache (in memory) → Still has all rules
   - ✅ Redis (for counters) → Still working
   - ✅ No database reads needed!

2. **Admin API (the control plane)** needs:
   - ❌ PostgreSQL primary (for writes) → DOWN
   - ✅ But this doesn't affect rate limiting at all

**So requests keep flowing:**

```
Client → RateLimitHandler → RuleCache (memory) → Redis → Decision
                            ↑
                            No database access!
```

**The only thing that's broken is the ability to create/update/delete rules.**

---

### **What Does the System Do?**

**Immediate actions:**

1. **Emit metrics:**

   ```
   admin_errors_total{operation="update_rule", error="db_connection_failed"} = 1
   ```

2. **Emit a trace** showing the failure:

   ```
   Span: UpdateRule
     - Status: ERROR
     - Error: "failed to begin transaction: connection refused"
     - Duration: 100ms (mostly connection timeout)
   ```

3. **Health check changes:**
   ```
   GET /health/admin
   {
     "status": "unhealthy",
     "postgres_primary": "unreachable",
     "rate_limiting": "healthy"
   }
   ```

**Alerting:**

- Prometheus alert fires: "Admin API database errors > 5 in 1 minute"
- PagerDuty pages: "PostgreSQL primary unreachable"
- On-call engineer investigates

**The operator's options:**

1. **Retry the request** (maybe Postgres just restarted and will be back in 10 seconds)
2. **Check the database status** (is the primary crashed? Failing over?)
3. **Wait for on-call to fix it**

---

### **What If the Operator Retries?**

**10:01:00 - Operator retries the same request:**

```http
PUT /admin/rules
{
  "tenantID": "acme",
  "resource": "/api/search",
  "limit": 500,
  "expectedVersion": 7
}
```

**If Postgres is still down:**

- Same error: `connection refused`
- Nothing changes

**If Postgres came back online:**

- The update succeeds!
- We proceed to the next steps (which we'll explore)

---

## Failure Scenario 2: Optimistic Locking Conflict

### **10:00:01 - Postgres Is Up, But There's a Race**

Let's say Postgres is healthy, but **two operators** are trying to update the same rule simultaneously:

**Operator A** (on Instance #3):

```json
{ "limit": 500, "expectedVersion": 7 }
```

**Operator B** (on Instance #7):

```json
{ "limit": 200, "expectedVersion": 7 }
```

Both arrive within milliseconds of each other.

---

**Inside `RuleDB.Update()` on Instance #3:**

```go
func (db *PostgresRuleDB) Update(ctx context.Context, rule *Rule, expectedVersion int64) (*Rule, error) {
    tx, err := db.primary.BeginTx(ctx, nil)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()

    // The critical UPDATE query with optimistic locking
    query := `
        UPDATE rules
        SET
            limit = $1,
            window = $2,
            burst_size = $3,
            version = version + 1,
            updated_at = NOW()
        WHERE
            tenant_id = $4
            AND resource = $5
            AND version = $6
        RETURNING *
    `

    var updated Rule
    err = tx.QueryRow(query,
        rule.Limit, rule.Window, rule.BurstSize,
        rule.TenantID, rule.Resource, expectedVersion,
    ).Scan(&updated.TenantID, &updated.Resource, ..., &updated.Version)

    if err == sql.ErrNoRows {
        // ❌ The version didn't match! Someone else updated it.
        return nil, ErrVersionConflict
    }

    // If we get here, the update succeeded
    // ... (insert into outbox, commit transaction)
}
```

**Timeline:**

- **T0**: Both transactions start
- **T1**: Instance #3 executes UPDATE → Succeeds! Version 7 → 8, limit set to 500
- **T2**: Instance #7 executes UPDATE with `WHERE version = 7` → **No rows match!** (version is now 8)
- **T3**: Instance #7 gets `sql.ErrNoRows` → Returns `ErrVersionConflict`

---

**Operator B gets an HTTP 409 response:**

```json
{
  "error": "version conflict: expected version 7, but rule was already updated",
  "currentVersion": 8
}
```

---

### **What Does This Mean?**

**This is CORRECT behavior!** Optimistic locking prevented a lost update.

**Operator B's options:**

1. **Fetch the current rule:**

   ```http
   GET /admin/rules?tenantID=acme&resource=/api/search
   ```

   Response:

   ```json
   {
     "limit": 500,
     "version": 8
   }
   ```

2. **Decide what to do:**

   - "Oh, Operator A already increased it to 500. I wanted 200, but maybe 500 is fine."
   - OR: "I still want 200. Let me update with expectedVersion=8."

3. **Retry with the new version:**
   ```json
   { "limit": 200, "expectedVersion": 8 }
   ```

**This prevents:**

- Lost updates (both operators' changes being applied unpredictably)
- Data corruption (version numbers getting out of sync)

---

## Failure Scenario 3: Transaction Commits, But Outbox Insert Fails

### **The Nightmare Scenario**

Inside `RuleDB.Update()`, after the rule update succeeds:

```go
// Step 1: Update the rule ✅
err = tx.QueryRow(updateQuery, ...).Scan(&updated)
if err != nil {
    return nil, err
}

// Step 2: Insert into the outbox ❌
outboxQuery := `
    INSERT INTO outbox (id, channel, payload, created_at)
    VALUES ($1, $2, $3, NOW())
`
_, err = tx.Exec(outboxQuery, uuid.New(), "rule_changes", payload)
if err != nil {
    // ❌ The outbox insert failed!
    // The transaction will rollback (because of defer tx.Rollback())
    return nil, fmt.Errorf("failed to insert outbox: %w", err)
}

// Step 3: Commit the transaction
err = tx.Commit()
if err != nil {
    return nil, err
}
```

**Wait, but this is inside a transaction!**

**If the outbox insert fails, the ENTIRE transaction rolls back:**

- The rule update is **undone**
- The version stays at 7
- Nothing changed

**The operator gets an error:**

```json
{
  "error": "failed to insert outbox: constraint violation"
}
```

**And they can retry safely!**

---

### **But What If the Outbox Succeeds, But Commit Fails?**

**A more insidious scenario:**

```go
// Step 1: Update rule ✅
// Step 2: Insert outbox ✅
err = tx.Commit()
if err != nil {
    // ❌ Commit failed! Maybe network partition, maybe Postgres crashed mid-commit
    return nil, err
}
```

**What happens here?**

**If `Commit()` returns an error, you DON'T KNOW if it committed or not!**

This is the famous "commit uncertainty" problem in distributed systems.

**Possible outcomes:**

1. **Transaction aborted**: Nothing changed, safe to retry
2. **Transaction committed**: The rule WAS updated (version 8, limit 500), and the outbox row WAS inserted
3. **Truly unknown**: The network died mid-commit, Postgres might still be processing it

---

### **How Does the Operator Handle This?**

**The operator gets an error:**

```json
{
  "error": "failed to commit transaction: connection lost"
}
```

**What should they do?**

**Option 1: Check if it worked**

```http
GET /admin/rules?tenantID=acme&resource=/api/search
```

**If the response is:**

```json
{ "limit": 500, "version": 8 }
```

**Then it DID commit!** Don't retry, or you'll get a version conflict.

**If the response is:**

```json
{ "limit": 100, "version": 7 }
```

**Then it DIDN'T commit.** Safe to retry.

---

**Option 2: Use idempotency keys (for CREATE operations)**

Notice the `CreateRuleRequest` has:

```go
type CreateRuleRequest struct {
    // ...
    IdempotencyKey string
}
```

**For CREATE operations:**

```go
func (db *PostgresRuleDB) Create(ctx context.Context, rule *Rule, idempotencyKey string) (*Rule, error) {
    // First, check if we already processed this idempotency key
    var existing Rule
    err := db.primary.QueryRow(`
        SELECT * FROM rules WHERE idempotency_key = $1
    `, idempotencyKey).Scan(&existing)

    if err == nil {
        // We already created this rule! Return the existing one.
        return &existing, nil
    }

    // Otherwise, proceed with creation
    // ...
}
```

**This makes CREATE idempotent:**

- Same idempotency key → Get the same rule back, no duplicate creation

**But UPDATE is trickier** because the expected version changes each time. You'd need to include the expected version in the idempotency key or use a separate idempotency table.

---

## Failure Scenario 4: Outbox Publisher Is Down

### **10:00:02 - The Transaction Committed Successfully**

The rule is now in the database:

- Version: 8
- Limit: 500
- An outbox row exists: `{"action": "upsert", "version": 8, "tenantID": "acme", "resource": "/api/search"}`

**The operator gets a success response!**

But now we need to **propagate this change** to all instances across all regions.

---

### **The Outbox Publisher's Job**

Remember, the **OutboxPublisher** runs on a loop:

```go
func (p *OutboxPublisherV3) Start(ctx context.Context) {
    ticker := time.NewTicker(100 * time.Millisecond)
    for {
        select {
        case <-ticker.C:
            p.publishPending(ctx)
        case <-ctx.Done():
            return
        }
    }
}

func (p *OutboxPublisherV3) publishPending(ctx context.Context) {
    // Step 1: Fetch unsent messages
    rows, err := p.outbox.FetchPending(ctx, 100)
    if err != nil {
        log.Error("failed to fetch outbox rows", err)
        return
    }

    // Step 2: Publish each one
    for _, row := range rows {
        err := p.pubsub.Publish(ctx, p.channel, row.Data)
        if err != nil {
            log.Error("failed to publish", row.ID, err)
            continue  // ⚠️ Don't mark as sent!
        }

        // Step 3: Mark as sent
        p.outbox.MarkSent(ctx, row.ID)
    }
}
```

---

### **Failure: The OutboxPublisher Process Crashes**

**What if Instance #3 (where the admin request landed) crashes RIGHT AFTER the transaction commits?**

**Timeline:**

- **10:00:01.500**: Transaction commits (rule v8 in DB, outbox row inserted)
- **10:00:01.501**: AdminHandler returns success to operator
- **10:00:01.502**: ❌ Instance #3 crashes (kernel panic, OOM, Kubernetes eviction)

**The outbox row is in the database, but it was never published to Redis Pub/Sub!**

---

### **What Happens Next?**

**Kubernetes restarts Instance #3** (or another instance takes over).

**10:00:15 - Instance #3 comes back online:**

When the new instance starts:

```go
func (a *ApplicationV3) Start(ctx context.Context) error {
    // ... (initialize everything)

    // Start the outbox publisher
    go a.outboxPub.Start(ctx)

    // ...
}
```

**The OutboxPublisher starts running:**

On its very first tick (100ms after startup):

```go
rows, err := p.outbox.FetchPending(ctx, 100)
```

**What does `FetchPending()` return?**

```go
func (o *PostgresOutbox) FetchPending(ctx context.Context, limit int) ([]OutboxRow, error) {
    query := `
        SELECT id, payload
        FROM outbox
        WHERE sent_at IS NULL
        ORDER BY created_at ASC
        LIMIT $1
    `

    // This query finds our orphaned row!
    rows, err := o.db.Query(query, limit)
    // ...
}
```

**It finds the unsent outbox row from the crashed instance!**

---

**The publisher then:**

1. Publishes it to Redis Pub/Sub
2. Marks it as sent: `UPDATE outbox SET sent_at = NOW() WHERE id = $1`

**All instances receive the invalidation event** and update their caches!

---

### **How Long Was the Delay?**

**From when the rule was committed to when it was published:**

- Commit: 10:00:01.500
- Instance crash: 10:00:01.502
- Instance restart: ~10:00:15 (Kubernetes restart time)
- First outbox poll: 10:00:15.100
- Published: 10:00:15.101

**Total delay: ~13.6 seconds**

---

### **What Does This Mean for Consistency?**

**During those 13.6 seconds:**

- The database has version 8 (limit 500)
- All instances' RuleCaches have version 7 (limit 100)
- Requests are being rate limited at 100/min instead of 500/min

**Is this acceptable?**

**It depends:**

1. **The full sync worker is still running** (every 10 seconds)

   - At 10:00:11, it would have loaded all rules from the replica
   - If the replica had replicated by then, instances would get version 8
   - So the delay might only be ~1 second!

2. **The operator increased the limit** (more permissive)

   - Users are getting rate limited MORE strictly than they should
   - But they're not getting unlimited access (which would be worse)

3. **This is bounded by the full sync interval**
   - Worst case: 10 seconds until the next full sync
   - Plus replication lag: ~1 second
   - Total: ~11 seconds max

---

## Failure Scenario 5: Redis Pub/Sub Is Down

### **10:00:02 - Outbox Publisher Tries to Publish**

```go
err := p.pubsub.Publish(ctx, "rule_changes", payload)
if err != nil {
    log.Error("failed to publish", row.ID, err)
    continue  // ⚠️ Don't mark as sent!
}
```

**The Pub/Sub call fails:**

```
Error: READONLY You can't write against a read only replica
```

(Redis went into read-only mode, or the cluster is down)

---

### **What Happens?**

**The outbox row is NOT marked as sent.**

The publisher logs the error and moves on to the next row.

**On the next tick (100ms later):**

```go
rows, err := p.outbox.FetchPending(ctx, 100)
```

**The same row is fetched again** (because `sent_at IS NULL`).

**The publisher retries:**

```go
err := p.pubsub.Publish(ctx, "rule_changes", payload)
```

**If Redis is still down, it fails again.**

**This continues indefinitely until Redis comes back.**

---

### **Does This Block New Rule Updates?**

**No!** New rule updates can still be written to the database.

**The outbox just accumulates rows:**

```
| id   | payload                     | sent_at |
|------|-----------------------------|---------|
| uuid1| {version: 8, limit: 500}    | NULL    |  ← Stuck, Redis down
| uuid2| {version: 9, limit: 600}    | NULL    |  ← New update, also stuck
| uuid3| {version: 10, limit: 700}   | NULL    |  ← Another update
```

**When Redis comes back:**

The publisher processes them **in order**:

1. Publishes version 8
2. Publishes version 9
3. Publishes version 10

**Each instance's cache invalidator receives all three events** and updates accordingly.

---

### **But Wait, Doesn't the Full Sync Save Us?**

**Yes!**

Even if Redis Pub/Sub is down for minutes, the **CacheSyncWorker** is still running every 10 seconds:

```go
func (w *CacheSyncWorkerV3) Start(ctx context.Context) {
    ticker := time.NewTicker(w.interval)  // 10 seconds
    for {
        select {
        case <-ticker.C:
            w.sync(ctx)
        }
    }
}

func (w *CacheSyncWorkerV3) sync(ctx context.Context) {
    // Load all rules from the read replica
    rules, err := w.db.LoadAll(ctx)
    if err != nil {
        log.Error("full sync failed", err)
        return
    }

    // Replace the entire cache
    w.rules.ReplaceAll(rules)
}
```

**So even if Pub/Sub is down, within 10 seconds (+ replication lag), all instances have the latest rules!**

**The outbox is just an optimization for fast convergence** (<100ms instead of 10 seconds).

---

## Failure Scenario 6: The Read Replica Is Lagging

### **10:00:02 - The Outbox Publisher Publishes the Event**

Redis Pub/Sub is healthy. The event is broadcast:

```json
{
  "tenantID": "acme",
  "resource": "/api/search",
  "action": "upsert",
  "version": 8
}
```

---

### **10:00:02.050 - Instance #7 Receives the Event**

The **CacheInvalidator** on Instance #7 handles it:

```go
func (ci *CacheInvalidatorV3) Subscribe(ctx context.Context) {
    ci.pubsub.Subscribe(ctx, ci.channel, func(payload []byte) {
        var event InvalidationEvent
        json.Unmarshal(payload, &event)

        if event.Action == "upsert" {
            // Fetch the latest rule from the replica
            rule, err := ci.db.Get(ctx, event.TenantID, event.Resource)
            if err != nil {
                log.Error("failed to fetch rule", err)
                // ❌ What do we do now?
            }

            // Upsert if newer
            ci.rules.UpsertIfNewer(rule)

            // Cutover limiters
            ci.pool.Cutover(ctx, event.TenantID, event.Resource, event.Version)
        }
    })
}
```

---

**Inside `db.Get()` on Instance #7:**

Instance #7 is in EU-West region. It queries its **local read replica**.

```sql
SELECT * FROM rules
WHERE tenant_id = 'acme' AND resource = '/api/search'
```

**But the replica hasn't replicated yet!**

**The replica returns:**

```json
{
  "version": 7,
  "limit": 100
}
```

**Not version 8!**

---

### **What Does the Invalidator Do?**

```go
rule, err := ci.db.Get(ctx, event.TenantID, event.Resource)
// rule.Version = 7

ci.rules.UpsertIfNewer(rule)
```

**Inside `UpsertIfNewer()`:**

```go
func (rc *RuleCacheV3) UpsertIfNewer(rule *Rule) {
    rc.mu.Lock()
    defer rc.mu.Unlock()

    // Load the current snapshot
    snap := rc.snap.Load().(*GlobalRulesSnapshot)

    // Find the current version
    tenantSnap := snap.byTenant[rule.TenantID]
    currentRule := tenantSnap.byResource[rule.Resource]

    if currentRule != nil && currentRule.Version >= rule.Version {
        // The current rule is newer or equal. Don't update.
        return
    }

    // ... (update logic)
}
```

**The comparison:**

- Event version: 8
- Fetched rule version: 7
- Current cache version: 7

**Since `7 >= 7`, the cache is NOT updated!**

**But then we call:**

```go
ci.pool.Cutover(ctx, event.TenantID, event.Resource, event.Version)
```

**Using the event's version (8), not the fetched rule's version (7).**

---

### **The Cutover Proceeds Anyway**

The **LimiterPool.Cutover()** is told: "Version 8 is here, cutover the limiter."

```go
func (p *LimiterPoolV3) Cutover(ctx context.Context, tenantID, resource string, newRuleVersion int64) {
    // Find the existing limiter
    key := limiterPoolKey(tenantID, resource)
    shard := p.shards[hash(key) % len(p.shards)]

    shard.mu.Lock()
    entry := shard.m[key]

    if entry == nil {
        // No limiter exists. Nothing to cutover.
        shard.mu.Unlock()
        return
    }

    // Mark it as quiescing
    entry.state.Store(int32(EntryQuiescing))
    shard.mu.Unlock()

    // Wait for the quiesce window
    time.Sleep(p.policy.QuiesceWindow)  // e.g., 2 seconds

    // Remove it from the map
    shard.mu.Lock()
    delete(shard.m, key)
    shard.mu.Unlock()

    // Close the limiter
    entry.lim.Close()
}
```

**The old limiter (version 7, limit 100) is removed.**

---

**10:00:04.050 - A new request arrives for acme + /api/search:**

The **RateLimitHandler** tries to get a limiter:

```go
handle, rule, err := p.pool.Acquire(ctx, "acme", "/api/search")
```

**Inside `Acquire()`:**

```go
// Check if we have a limiter in the pool
entry := shard.m[key]

if entry == nil || entry.state.Load() != int32(EntryActive) {
    // No active limiter. Create a new one.
    rule, ok := p.rules.Get(tenantID, resource)
    if !ok {
        return nil, nil, ErrRuleNotFound
    }

    // Create a limiter for this rule
    lim, params, err := p.factory.Create(rule)
    // ...
}
```

**What rule does it fetch from the cache?**

**Version 7, limit 100!**

**Because the replica hadn't replicated yet, and `UpsertIfNewer` rejected version 7.**

---

### **The Problem**

**We cut over the limiter but didn't update the rule in the cache!**

- Old limiter (v7, limit 100): Destroyed
- New limiter (v7, limit 100): Created with the same parameters!

**We just did a no-op cutover.**

**The limit is still 100, not 500.**

---

### **When Does It Actually Update?**

**Option 1: The replica catches up**

**10:00:03 - The replica finally replicates:**

The next invalidation event (if there is one) or the next full sync will fetch the correct version.

**Or, the CacheSyncWorker runs:**

**10:00:10 - Full sync:**

```go
rules, err := w.db.LoadAll(ctx)
// Now the replica has version 8!

w.rules.ReplaceAll(rules)
```

**The cache is updated to version 8.**

**But the limiter is still version 7** (created at 10:00:04).

**10:00:11 - A new request arrives:**

```go
handle, rule, err := p.pool.Acquire(ctx, "acme", "/api/search")
```

**Does it create a new limiter?**

**No!** The limiter from 10:00:04 is still in the pool (it's cached).

```go
entry := shard.m[key]

if entry != nil && entry.state.Load() == int32(EntryActive) {
    // Check if the rule version changed
    currentRule, _ := p.rules.Get(tenantID, resource)

    if entry.lim.RuleVersion() != currentRule.Version {
        // The rule changed! Trigger a cutover.
        go p.Cutover(ctx, tenantID, resource, currentRule.Version)

        // But for THIS request, we'll use the old limiter
        // (or we could wait for the cutover, but that adds latency)
    }

    // Return the existing limiter
    // ...
}
```

**Ah, but this check is NOT in the current design!**

Let me re-read the `Acquire()` code...

---

**Actually, looking at the skeleton, `Acquire()` doesn't check rule versions.** It just returns what's in the pool.

**So the limiter with version 7 (limit 100) will keep running until:**

1. **Another invalidation event arrives** (for a different rule change)
2. **The limiter is evicted from the LRU** (due to memory pressure)
3. **The instance restarts**

---

### **The Design Flaw**

**The current design assumes:**

When an invalidation event arrives, the replica will have the new rule.

**But this is not guaranteed!**

Replication lag can be:

- 10ms (normal)
- 100ms (busy database)
- 1 second (heavy load)
- 10 seconds (network issues)

---

### **How Do We Fix This?**

**Option 1: Trust the event, don't fetch**

```go
if event.Action == "upsert" {
    // Don't fetch from the replica. Just wait for full sync.
    // But still trigger the cutover so the next request creates a fresh limiter.
    ci.pool.Cutover(ctx, event.TenantID, event.Resource, event.Version)
}
```

**Pro:** Avoids the replica lag issue.
**Con:** The cache won't have the new rule until the next full sync (up to 10 seconds).

---

**Option 2: Retry the fetch with backoff**

```go
if event.Action == "upsert" {
    var rule *Rule
    var err error

    for i := 0; i < 3; i++ {
        rule, err = ci.db.Get(ctx, event.TenantID, event.Resource)
        if err == nil && rule.Version >= event.Version {
            // We got the right version!
            break
        }

        // Replica is lagging. Wait a bit.
        time.Sleep(100 * time.Millisecond * (1 << i))  // 100ms, 200ms, 400ms
    }

    if rule != nil && rule.Version >= event.Version {
        ci.rules.UpsertIfNewer(rule)
    }

    // Cutover anyway (using the event version)
    ci.pool.Cutover(ctx, event.TenantID, event.Resource, event.Version)
}
```

**Pro:** Usually gets the right version within a few hundred milliseconds.
**Con:** Adds complexity and latency to the invalidation path.

---

**Option 3: Accept eventual consistency**

```go
// In Acquire(), check if the limiter's version matches the rule's version
if entry != nil && entry.state.Load() == int32(
```

EntryActive) {
currentRule, \_ := p.rules.Get(tenantID, resource)

    if entry.lim.RuleVersion() != currentRule.Version {
        // Rule changed. Trigger a cutover in the background.
        go p.Cutover(ctx, tenantID, resource, currentRule.Version)
    }

    // Use the existing limiter for now (eventually consistent)

}

````

**Pro:** Simple, eventually correct.
**Con:** Requests might use stale limits for a few seconds.

---

### **Which Option Is Best?**

**It depends on your consistency requirements:**

- **Strict (within 1 second)**: Use Option 2 (retry with backoff)
- **Relaxed (within 10 seconds)**: Use Option 1 or 3

**Remember, the design already accepts 10-second eventual consistency** (the full sync interval), so Option 1 or 3 aligns with that philosophy.

---

## Failure Scenario 7: An Instance Misses the Invalidation Event

### **10:00:02 - Instance #5 Is Restarting**

Redis Pub/Sub broadcasts the invalidation event.

**9 out of 10 instances receive it.**

**Instance #5 was in the middle of a restart:**
- Kubernetes killed it at 10:00:01
- It's restarting, Go runtime initializing
- The Pub/Sub subscription isn't active yet

**The event is lost for Instance #5.**

---

### **What Happens?**

**Instance #5 comes online at 10:00:05:**

```go
func (a *ApplicationV3) Start(ctx context.Context) error {
    // Load all rules from the replica (full sync on startup)
    rules, err := a.cfg.RuleDB.LoadAll(ctx)
    if err != nil {
        return err
    }

    a.rules.ReplaceAll(rules)

    // Start the invalidation subscriber
    go a.invalid.Subscribe(ctx)

    // Start the full sync worker
    go a.syncer.Start(ctx)

    // ...
}
````

**On startup, it does a full sync!**

If the replica has replicated by now (likely, after 4 seconds), Instance #5 loads version 8 (limit 500).

**So even though it missed the invalidation event, it's consistent.**

---

### **But What If the Replica Is Still Lagging?**

**Instance #5 loads version 7 (limit 100) on startup.**

**It starts serving requests with the old limit.**

**10:00:15 - The CacheSyncWorker runs:**

It loads version 8 and updates the cache.

**Max staleness: 10 seconds** (from the last full sync to the next one).

---

## Failure Scenario 8: The Entire Region Is Partitioned

### **10:02:00 - Network Partition**

The US-East region is **completely cut off** from the rest of the infrastructure:

- Can't reach the PostgreSQL primary (in US-East)
- Can't reach Redis Pub/Sub
- Can't reach read replicas in other regions

**But the region is internally healthy:**

- All 10 instances are up
- The regional Redis cluster is up
- The local read replica is up (but can't replicate from the primary)

---

### **What Happens to the Admin API?**

**An operator tries to update a rule:**

```http
PUT /admin/rules (on Instance #3 in US-East)
```

**Inside `RuleDB.Update()`:**

```go
tx, err := db.primary.BeginTx(ctx, nil)
```

**Error: `connection refused` (can't reach the primary in the other region)**

**The admin API is DOWN in US-East.**

---

### **What Happens to the Rate Limiting API?**

**Requests keep flowing in:**

```
Client → Instance #3 → RateLimitHandler
```

**Step 1: Fetch rule from cache**

```go
rule, ok := rules.Get("acme", "/api/search")
// ✅ Cache is in memory, no network needed
```

**Step 2: Get limiter from pool**

```go
handle, rule, err := pool.Acquire(ctx, "acme", "/api/search")
// ✅ Pool is in memory, no network needed
```

**Step 3: Call Redis**

```go
decision, err := redis.ExecTokenBucket(ctx, key, params, cost)
// ✅ Regional Redis cluster is UP and reachable!
```

**Step 4: Return decision**

```json
{ "allowed": true, "remaining": 42 }
```

**Rate limiting works perfectly!**

---

### **But New Rule Changes Don't Propagate**

**Meanwhile, in EU-West (which can still reach the primary):**

An operator updates the same rule:

```json
{ "limit": 1000, "version": 8 }
```

**The update succeeds:**

- Postgres primary: ✅ Updated to version 8
- Outbox: ✅ Row inserted
- Redis Pub/Sub: ✅ Event published

**EU-West instances receive the event and update to version 8.**

**But US-East instances never receive it** (Pub/Sub is unreachable).

---

### **US-East Is Now Stale**

**US-East:**

- Version 7, limit 100

**EU-West:**

- Version 8, limit 1000

**Each region rate limits independently** (remember, counters are per-region in Redis).

**So users in US-East get 100 req/min, users in EU-West get 1000 req/min.**

---

### **When the Partition Heals**

**10:15:00 - Network is restored**

**The CacheSyncWorker runs at 10:15:10:**

```go
rules, err := w.db.LoadAll(ctx)
// Now US-East can reach its read replica again
// The replica has replicated version 8
```

**US-East updates to version 8 (limit 1000).**

**Consistency restored!**

---

### **The Key Insight**

**The system is designed for regional autonomy:**

- Each region operates independently
- If a region is cut off, it keeps working with stale rules
- When connectivity is restored, it converges via full sync

**This is EVENTUAL consistency by design.**

**The trade-off:**

- ✅ High availability (regions don't depend on each other)
- ❌ Temporary inconsistency during partitions

---

## Summary: What Does the System Do When Things Fail?

Let me summarize all the failure modes:

| **Failure**                       | **Impact on Rate Limiting**        | **Impact on Admin API**   | **Recovery**                                                                   |
| --------------------------------- | ---------------------------------- | ------------------------- | ------------------------------------------------------------------------------ |
| **Postgres primary down**         | None (uses cache + Redis)          | DOWN (can't write rules)  | Operator retries when Postgres is back                                         |
| **Optimistic lock conflict**      | None                               | Returns 409 to operator   | Operator fetches latest and retries                                            |
| **Transaction fails**             | None                               | Returns error to operator | Operator retries (idempotent)                                                  |
| **Instance crashes after commit** | Delayed propagation (~10s)         | None                      | Outbox publisher on another instance picks up the row                          |
| **Redis Pub/Sub down**            | None                               | None                      | Events queue in outbox, delivered when Pub/Sub is back. Full sync still works. |
| **Read replica lagging**          | Stale limits for up to 10s         | None                      | Full sync catches up                                                           |
| **Instance misses event**         | Stale limits until next full sync  | None                      | Full sync on startup + periodic sync                                           |
| **Region partitioned**            | Stale limits until partition heals | DOWN in that region       | Full sync when connectivity restored                                           |

---

## The Design Philosophy on Failures

**The system has layers of resilience:**

1. **Optimistic path:** Invalidation events (<100ms propagation)
2. **Pessimistic path:** Full sync every 10 seconds
3. **Disaster path:** Operate with stale rules (eventual consistency)

**The system NEVER:**

- Stops rate limiting (even if all of Postgres is down)
- Allows unbounded traffic (fallback limiters are bounded)
- Loses data (transactions + outbox pattern)

**The system DOES:**

- Tolerate stale rules for up to 10 seconds (full sync interval)
- Tolerate regional autonomy (eventual consistency)
- Require manual retry for admin operations (no automatic retry to avoid duplicate writes)

---

Does this detailed failure analysis help? Would you like me to explore:

1. **What happens when Redis (for counters) goes down during an active rule update?** (combining both failure modes)
2. **How to implement exactly-once delivery for the outbox pattern** (to avoid duplicate invalidations)
3. **Split-brain scenarios** (what if membership thinks there are 5 instances but there are actually 10?)
4. **Cascading failures** (what if Postgres AND Redis both go down?)
