# Act 1: The Hot Path When Redis Goes Down

Let me tell you what happens when disaster strikes.

---

## The Scene Before the Failure

**9:47 AM, US-East region, a normal Tuesday:**

Your rate limiter service is humming along. You have 10 instances running behind a load balancer, each handling ~10,000 requests per second. Everything is healthy:

- Redis cluster: ✅ Responding in <1ms
- Postgres read replica: ✅ Up and serving rule queries
- All 10 instances: ✅ Happily processing requests

Requests flow in:

```
"TenantID: acme, UserID: alice, Resource: /api/search, Cost: 1"
```

The **RateLimitHandler** does its normal dance:

1. Looks up the rule in **RuleCache** (in memory) → "token_bucket, 100 req/min"
2. Gets a **TokenBucketLimiter** from the **LimiterPool**
3. The limiter calls **RedisClient.ExecTokenBucket()**
4. Redis runs the Lua script, responds: `{allowed: true, remaining: 47}`
5. Handler returns success to the client

Life is good.

---

## 9:48 AM: Redis Goes Down

**What actually happened:**

The Redis cluster in US-East suffers a cascading failure. Maybe:

- A config change went wrong
- Network partition isolated the cluster
- All Redis nodes ran out of memory and OOM-killed themselves

**The symptom:**

All 10 instances of your rate limiter start seeing errors when they try to call Redis:

```
Error: dial tcp 10.0.5.123:6379: connect: connection refused
```

---

## Step-by-Step: What Happens Next

### **9:48:00 - First Redis Failures**

**Instance #3** is processing a request for Alice from Acme. It reaches the critical moment:

```go
// Inside TokenBucketLimiter.Allow()
decision, err := redis.ExecTokenBucket(ctx, key, params, cost)
```

**Redis returns an error.**

**What does the limiter do?**

The **TokenBucketLimiter** doesn't panic. It has fallback logic:

```go
if err != nil {
    // Redis failed. Check the operating mode.
    mode := degradeController.Mode()

    if mode == ModeNormal {
        // We're still in normal mode, but this specific call failed.
        // Fall back to the FallbackLimiter.
        decision = fallbackLimiter.Allow(ctx, key, params, cost)
    }
}
```

**So even though the system is still in `ModeNormal` (because the health loop hasn't detected the failure yet), this individual request falls back.**

But let's hold that thought—let's first see what happens system-wide.

---

### **9:48:01 - Health Loop Detects the Problem**

Every instance runs a **HealthLoop** that executes every 1 second:

```go
// Inside HealthLoop.Start()
ticker := time.NewTicker(1 * time.Second)
for {
    <-ticker.C
    degradeController.Update(ctx)
}
```

**What does `DegradeController.Update()` do?**

```go
func (dc *DegradeController) Update(ctx context.Context) {
    // Step 1: Check Redis health
    redisHealthy := dc.redis.Healthy(ctx)

    if !redisHealthy {
        dc.redisUnhealthySince = time.Now()
    } else {
        dc.redisUnhealthySince = nil
    }

    // Step 2: Calculate how long Redis has been unhealthy
    if dc.redisUnhealthySince != nil {
        unhealthyDuration := time.Since(dc.redisUnhealthySince)

        if unhealthyDuration > dc.thresholds.RedisUnhealthyFor {
            // Redis has been down for more than the threshold (e.g., 5 seconds)
            // Transition to Degraded mode
            dc.mode.Store(int32(ModeDegraded))
            return
        }
    }

    // If Redis is healthy again, go back to Normal
    if redisHealthy {
        dc.mode.Store(int32(ModeNormal))
    }
}
```

**Let's break this down:**

1. **`redis.Healthy(ctx)` is called**

   - This tries to execute a simple Redis command: `PING`
   - If it fails or times out (say, after 100ms), it returns `false`

2. **Redis is unhealthy**

   - The controller records the timestamp: `redisUnhealthySince = 9:48:01`

3. **But we don't switch modes immediately!**
   - Why? Because Redis might just have a brief hiccup. Maybe it's restarting, or there's a momentary network blip.
   - The threshold is set in config: `RedisUnhealthyFor: 5 seconds`

**So for the next 5 seconds (9:48:01 to 9:48:06), the system is still in `ModeNormal` but Redis calls are failing.**

---

### **9:48:01 to 9:48:06 - The Transition Period**

During these 5 seconds, requests keep coming in. Let's trace one:

**Request for Alice:**

1. **RateLimitHandler** gets the rule from **RuleCache** ✅
2. Gets a **TokenBucketLimiter** from **LimiterPool** ✅
3. Limiter tries to call **Redis** ❌ **Error!**

**Now what?**

The limiter immediately falls back:

```go
decision = fallbackLimiter.Allow(ctx, key, params, cost)
```

**This is the critical part: What is the FallbackLimiter?**

---

## Interlude: What IS the FallbackLimiter?

Let me explain why we need it and what it does.

### **The Problem We're Solving**

Imagine if Redis went down and we just returned an error:

```
"Sorry, I can't check your rate limit because Redis is down."
```

The client service has two bad choices:

1. **Deny the request**: Your entire system goes down because the rate limiter is down. Cascade failure!
2. **Allow the request**: Everyone gets unlimited access. Your backend gets crushed.

**We need a middle ground: continue rate limiting, but locally.**

---

### **Why Not Just Use the Normal Limiter?**

You asked: "Why are we getting a FallbackLimiter? The limiter didn't fail, Redis failed!"

Great question! Here's the thing:

The **TokenBucketLimiter** (the normal limiter) is just a thin wrapper around Redis. It doesn't store any state itself. Its `Allow()` method looks like:

```go
func (tbl *TokenBucketLimiter) Allow(ctx context.Context, key []byte, cost int64) (*Decision, error) {
    // All the logic is in Redis. We just call it.
    return tbl.redis.ExecTokenBucket(ctx, key, tbl.params, cost)
}
```

**Without Redis, it's useless.** It's like a phone without a network—it can't do its job.

**So the FallbackLimiter is a completely different implementation:**

Instead of storing state in Redis, it stores state **in memory, on this instance**.

```go
type FallbackLimiterV3 struct {
    local *LocalLimiterStoreV3  // ← In-memory token buckets, one per key

    ownership Ownership          // ← "Am I responsible for this key?"
    policy    FallbackPolicyV3   // ← How to behave in fallback mode
    mode      *DegradeController // ← What mode are we in?
}
```

---

## Back to the Story: What Does FallbackLimiter.Allow() Do?

**9:48:03 - A request for Alice arrives at Instance #3**

The TokenBucketLimiter failed to reach Redis. It calls:

```go
decision = fallbackLimiter.Allow(ctx, key, params, cost)
```

**Inside FallbackLimiter.Allow():**

```go
func (f *FallbackLimiterV3) Allow(ctx context.Context, key []byte, params RuleParams, cost int64) *Decision {
    // Step 1: What mode are we in?
    currentMode := f.mode.Mode()

    // Step 2: Am I the owner of this key?
    isOwner := f.ownership.IsOwner(ctx, key)

    // Step 3: Decide what to do based on mode and ownership
    switch currentMode {
    case ModeNormal:
        // Redis just failed for this one request, but we're still in normal mode
        if isOwner {
            // Yes, I'm the owner. Use my local limiter.
            return f.local.Allow(key, params, cost)
        } else {
            // No, I'm not the owner. Deny the request.
            return &Decision{
                Allowed: false,
                RetryAfter: 1 * time.Second,
            }
        }

    case ModeDegraded:
        // The system has officially entered degraded mode
        if isOwner {
            return f.local.Allow(key, params, cost)
        } else {
            if f.policy.DenyWhenNotOwner {
                return &Decision{Allowed: false}
            } else {
                // Risky: allow anyway (might multiply limits)
                return f.local.Allow(key, params, cost)
            }
        }

    case ModeEmergency:
        // Everything is on fire
        if f.policy.EmergencyAllowSmallCap {
            // Allow a tiny local cap
            emergencyParams := RuleParams{
                Limit: f.policy.EmergencyCapPerWindow,
                Window: params.Window,
            }
            return f.local.Allow(key, emergencyParams, cost)
        } else {
            // Deny everything
            return &Decision{Allowed: false}
        }
    }
}
```

**Let's break this down piece by piece.**

---

## Concept: Ownership (RendezvousOwnership)

You have 10 instances. Redis is down. Alice's request could land on **any** of the 10 instances (load balancer distributes randomly).

**The disaster scenario without ownership:**

- Alice's limit is 100 requests/minute
- Her requests are distributed across 10 instances
- Each instance has a local in-memory limiter
- **Each instance allows 100 requests/minute**
- **Total: Alice can make 1,000 requests/minute!** (10x multiplied limit)

**This defeats the entire purpose of rate limiting!**

---

### **The Solution: Only ONE Instance Is the "Owner"**

For every unique key (like `"acme:alice:/api/search"`), we deterministically choose **one instance** to be responsible for rate limiting that key.

**How do we choose?**

Using a technique called **Rendezvous Hashing** (also called "Highest Random Weight" hashing):

```go
type RendezvousOwnershipV3 struct {
    m Membership  // Knows the list of all instances
}

func (o *RendezvousOwnershipV3) IsOwner(ctx context.Context, key []byte) bool {
    // Step 1: Get all instances
    instances := o.m.Instances(ctx)  // ["instance-1", "instance-2", ..., "instance-10"]

    // Step 2: For each instance, compute a hash score
    var maxScore uint64
    var owner string

    for _, instance := range instances {
        // Combine the key and instance ID, then hash
        score := hash(key + instance)

        if score > maxScore {
            maxScore = score
            owner = instance
        }
    }

    // Step 3: Am I the owner?
    return owner == o.m.SelfID()
}
```

**What this means:**

- For the key `"acme:alice:/api/search"`, the hash scores might be:

  - instance-1: 3847592
  - instance-2: 9284756 ← **Highest!**
  - instance-3: 1847392
  - ...

- **Instance-2 is the owner**
- **All 10 instances compute the same answer** (same key, same hash function, same membership list)

**So when Alice's request lands on:**

- **Instance-2**: "I'm the owner! I'll check my local limiter."
- **Instance-7**: "I'm not the owner. I'll deny this request."

**Result:**

- Only instance-2 enforces the limit locally
- The limit is **not multiplied**
- Alice gets 100 req/min total, even though there are 10 instances

---

## Back to Our Request at 9:48:03

**Request for Alice lands on Instance #3.**

Inside `FallbackLimiter.Allow()`:

```go
isOwner := f.ownership.IsOwner(ctx, key)  // Runs rendezvous hash
// Result: false (instance-2 is the owner, not instance-3)

// We're in ModeNormal (system hasn't switched to Degraded yet)
if isOwner {
    // ...
} else {
    // I'm not the owner. Deny.
    return &Decision{
        Allowed: false,
        RetryAfter: 1 * time.Second,
    }
}
```

**Alice's request is denied!**

The client service gets back:

```json
{
  "allowed": false,
  "retryAfter": "1s"
}
```

The client waits 1 second and retries. The load balancer might send it to instance-2 this time.

---

**Now let's say the retry lands on Instance #2 (the owner):**

```go
isOwner := f.ownership.IsOwner(ctx, key)  // true!

// I'm the owner. Use my local limiter.
return f.local.Allow(key, params, cost)
```

**What is `f.local` (LocalLimiterStore)?**

It's an **in-memory store of token bucket limiters**, one per key:

```go
type LocalLimiterStoreV3 struct {
    mu       sync.RWMutex
    limiters map[string]*InMemoryTokenBucket
}

func (ls *LocalLimiterStoreV3) Allow(key []byte, params RuleParams, cost int64) *Decision {
    ls.mu.Lock()
    defer ls.mu.Unlock()

    keyStr := string(key)
    limiter := ls.limiters[keyStr]

    if limiter == nil {
        // First time seeing this key. Create a limiter.
        limiter = NewInMemoryTokenBucket(params.Limit, params.Window)
        ls.limiters[keyStr] = limiter
    }

    // Check if we can deduct tokens
    return limiter.Allow(cost)
}
```

**InMemoryTokenBucket** is a simple token bucket implementation:

```go
type InMemoryTokenBucket struct {
    limit      int64
    tokens     float64
    lastRefill time.Time
    window     time.Duration
}

func (tb *InMemoryTokenBucket) Allow(cost int64) *Decision {
    now := time.Now()

    // Refill tokens based on time elapsed
    elapsed := now.Sub(tb.lastRefill)
    tokensToAdd := (float64(tb.limit) / tb.window.Seconds()) * elapsed.Seconds()
    tb.tokens = math.Min(tb.tokens + tokensToAdd, float64(tb.limit))
    tb.lastRefill = now

    // Can we deduct?
    if tb.tokens >= float64(cost) {
        tb.tokens -= float64(cost)
        return &Decision{
            Allowed: true,
            Remaining: int64(tb.tokens),
            Limit: tb.limit,
        }
    } else {
        return &Decision{
            Allowed: false,
            RetryAfter: calculateRetryAfter(tb.tokens, cost, tb.limit, tb.window),
        }
    }
}
```

**So Alice's request is checked against an in-memory token bucket on instance-2, and if allowed, she gets through!**

---

## 9:48:06 - System Enters ModeDegraded

The **HealthLoop** has been checking Redis every second:

- 9:48:01: Redis unhealthy (mark timestamp)
- 9:48:02: Redis unhealthy (1 second down)
- 9:48:03: Redis unhealthy (2 seconds down)
- 9:48:04: Redis unhealthy (3 seconds down)
- 9:48:05: Redis unhealthy (4 seconds down)
- 9:48:06: Redis unhealthy (5 seconds down) ← **Threshold reached!**

```go
if unhealthyDuration > dc.thresholds.RedisUnhealthyFor {  // 5 seconds
    dc.mode.Store(int32(ModeDegraded))
    log.Warn("Entering ModeDegraded: Redis unhealthy for 5 seconds")
}
```

**What does `ModeDegraded` mean for the system?**

It's an **explicit state** that tells every component: "We know Redis is down. Operate accordingly."

In `ModeNormal`:

- We **expect** Redis to work
- If a Redis call fails, it's a transient error
- Fall back cautiously

In `ModeDegraded`:

- We **know** Redis is down
- Don't even try to call Redis
- Use fallback aggressively

---

## Concept: Circuit Breaker

You asked: "What does it mean for a circuit breaker to open?"

Let's talk about circuit breakers in this system.

**A circuit breaker is like a smart fuse:**

Imagine you have a lamp that's shorting out. If you keep flipping the switch, you'll keep getting shocked. A circuit breaker says: "I've detected a problem. I'm going to stop even trying for a while."

**In this system, the circuit breaker wraps Redis calls:**

```go
type CircuitBreaker struct {
    state       int32  // Closed, Open, HalfOpen
    failures    int32
    lastFailure time.Time
    threshold   int    // e.g., 5 failures
    timeout     time.Duration  // e.g., 30 seconds
}

func (cb *CircuitBreaker) Call(fn func() error) error {
    state := cb.getState()

    if state == Open {
        // The circuit is open. Don't even try the call.
        return ErrCircuitOpen
    }

    // Try the call
    err := fn()

    if err != nil {
        cb.recordFailure()
        if cb.failures >= cb.threshold {
            cb.state.Store(Open)
            cb.lastFailure = time.Now()
        }
        return err
    }

    // Success! Reset failures.
    cb.failures = 0
    if state == HalfOpen {
        cb.state.Store(Closed)
    }
    return nil
}
```

**The three states:**

1. **Closed** (normal): Everything is fine, calls go through
2. **Open** (tripped): Too many failures detected. **Stop trying!** Return error immediately without calling Redis.
3. **HalfOpen** (testing): After a timeout, try one call. If it succeeds, go back to Closed. If it fails, go back to Open.

---

**In `ModeDegraded`, the circuit breaker is likely already open:**

```go
// Inside TokenBucketLimiter.Allow()
if mode == ModeDegraded {
    // Don't even try Redis. Circuit is open.
    return fallbackLimiter.Allow(ctx, key, params, cost)
}
```

**What does "circuit breaker open" mean for the system?**

- **No Redis calls are attempted**: Saves network overhead, prevents further strain on Redis
- **All requests immediately fall back**: Lower latency (no waiting for timeouts)
- **Observability**: Metrics show `circuit_breaker_state=open`, alerts fire

**Eventually, after a timeout (say, 30 seconds), the circuit goes to HalfOpen:**

- One request tries Redis again
- If it succeeds: "Redis is back! Go to Closed, resume normal operation"
- If it fails: "Still down. Go back to Open."

---

## 9:48:07 onwards - Operating in Degraded Mode

Now that the system is in `ModeDegraded`, here's what happens for every request:

**Request for Alice lands on Instance #7:**

1. **RateLimitHandler** gets the rule ✅
2. Gets a **TokenBucketLimiter** from the pool ✅
3. Limiter checks the mode:

```go
mode := degradeController.Mode()  // ModeDegraded

if mode == ModeDegraded {
    // Skip Redis entirely. Go straight to fallback.
    decision = fallbackLimiter.Allow(ctx, key, params, cost)
}
```

4. **FallbackLimiter** checks ownership:

```go
isOwner := ownership.IsOwner(ctx, key)  // Rendezvous hash
// Instance-7 is not the owner of Alice's key

if !isOwner {
    if policy.DenyWhenNotOwner {
        // Configured to deny non-owners in degraded mode
        return &Decision{Allowed: false}
    }
}
```

5. **Response**: Denied (because instance-7 is not the owner)

---

**Request for Alice lands on Instance #2 (the owner):**

```go
isOwner := ownership.IsOwner(ctx, key)  // true!

return f.local.Allow(key, params, cost)  // Check in-memory token bucket
```

6. **In-memory token bucket** on instance-2 checks if Alice has tokens
7. If yes: `{allowed: true, remaining: 42}`
8. If no: `{allowed: false, retryAfter: 15s}`

---

## What the System Does After Redis Goes Down

**Does the system go offline?**

**No!** The entire point of this design is to **keep operating** even when Redis is down.

**Does it try to restart Redis?**

**No!** Your rate limiter service doesn't manage Redis. That's the job of:

- Your cloud provider (AWS ElastiCache, GCP Memorystore)
- Your Kubernetes operator (Redis Operator)
- Your on-call SRE (who gets paged)

**What the system DOES do:**

1. **Continue serving requests** using local in-memory limiters (with ownership to prevent multiplied limits)

2. **Keep trying Redis** in the background:

   - The circuit breaker goes to HalfOpen after 30 seconds
   - One request tries Redis
   - If Redis is back, the circuit closes and we transition back to `ModeNormal`

3. **Emit observability signals**:

   - Metrics: `operating_mode=degraded`, `fallback_requests_total`, `redis_errors_total`
   - Traces: Show that requests are using fallback paths
   - Logs: "Entered ModeDegraded at 9:48:06"
   - Health check endpoint: `/health` returns `status: degraded` (so load balancers know)

4. **Alert on-call engineers**:
   - Prometheus alerts fire: "Rate limiter in degraded mode for >1 minute"
   - PagerDuty pages the on-call person
   - Engineers investigate Redis

---

## What About Consistency During Degraded Mode?

**The elephant in the room:**

When Redis is down, each instance has its own in-memory limiters. These are **not synchronized**.

**What if an instance crashes during degraded mode?**

- Instance-2 (the owner of Alice's key) crashes at 9:50:00
- Alice has consumed 50 of her 100 tokens on instance-2's in-memory limiter
- Kubernetes restarts instance-2

**When instance-2 comes back up:**

- Its in-memory state is **gone**
- Alice effectively gets a **fresh 100 tokens**

**Is this acceptable?**

It depends on your use case:

- **For free-tier API limits**: Probably fine. Better than denying all requests.
- **For DDoS protection**: Probably fine. Most attacks don't coordinate instance restarts.
- **For billing-critical limits**: Maybe not. You might prefer `DenyWhenNotOwner=true` and accept downtime.

**The config knob:**

```go
type FallbackPolicyV3 struct {
    DenyWhenNotOwner bool  // true = safer, false = more available
}
```

---

## When Redis Comes Back: Transition to ModeNormal

**9:55:00 - Redis is back online**

Your SRE fixed the issue. Redis is healthy again.

**The HealthLoop detects it:**

```go
redisHealthy := dc.redis.Healthy(ctx)  // PING succeeds!

if redisHealthy {
    dc.mode.Store(int32(ModeNormal))
    dc.redisUnhealthySince = nil
    log.Info("Returning to ModeNormal: Redis is healthy")
}
```

**What happens to in-flight requests?**

- Requests currently being processed with fallback limiters complete normally
- **New requests** immediately start using Redis again

**What happens to the in-memory limiter state?**

- It stays in memory! The `LocalLimiterStoreV3` map doesn't get cleared.
- Over time (minutes/hours), keys expire or get garbage collected
- Next time Redis goes down, they'll be reused

**Is there a consistency issue?**

Let's say:

- Alice consumed 50 tokens from the in-memory limiter on instance-2 during degraded mode
- Redis comes back
- Alice's next request goes to instance-5
- Instance-5 calls Redis

**What does Redis know about Alice's recent requests?**

- Redis was down, so it **doesn't know about the 50 tokens consumed during degraded mode**
- Alice effectively gets a **free 50 tokens**

**This is a known trade-off:**

- **Perfect accuracy** vs. **availability**
- This design chooses availability
- The "leakage" is bounded by the degraded mode duration (hopefully minutes, not hours)

If you need perfect accuracy, you'd set `DenyWhenNotOwner=true` and accept downtime during Redis outages.

---

## Modes Compared: What Each One Means

Let me summarize the three modes:

### **ModeNormal** (Everything Is Fine)

- **What it means**: Redis is healthy, operating as designed
- **What happens on a request**:
  1. Check rule cache ✅
  2. Get limiter from pool ✅
  3. Call Redis ✅
  4. Return decision ✅
- **If Redis call fails**: Fall back to local limiter (with ownership), but this is rare

### **ModeDegraded** (Redis Is Known to Be Down)

- **What it means**: Redis has been unhealthy for >5 seconds, we're operating with reduced capabilities
- **What happens on a request**:
  1. Check rule cache ✅
  2. Get limiter from pool ✅
  3. **Skip Redis, go straight to fallback** ✅
  4. Check ownership ✅
  5. If owner: use local limiter ✅
  6. If not owner: deny or allow based on policy
- **Trade-off**: Accepting potential inaccuracy for availability

### **ModeEmergency** (Everything Is on Fire)

- **What it means**: Redis is down **AND** membership service is down (we don't even know who the owners are)
- **What happens on a request**:
  1. Check rule cache ✅
  2. Based on policy:
     - **Option A**: Allow a tiny cap per instance (e.g., 10 req/min regardless of rule)
     - **Option B**: Deny everything
- **When would you use this?**: Kubernetes is down, service discovery is down, total disaster
- **Trade-off**: Either multiply limits by N instances (risky) or deny all traffic (safe but offline)

---

## The Key Insight

The entire design revolves around **graceful degradation**:

- **Layer 1** (best): Redis is up, perfect accuracy
- **Layer 2** (good): Redis is down, use local limiters with ownership
- **Layer 3** (survival): Everything is down, either fail-open with caps or fail-closed

Each layer is **explicit** (modes are enums, not scattered booleans) and **observable** (you can see exactly what mode you're in).

---

Does this detailed walkthrough clarify how the system handles failures? Would you like me to explore:

1. **What happens when an instance crashes** (and how ownership shifts)
2. **The exact Lua script** that runs in Redis during normal mode
3. **The transition from Degraded back to Normal** (and potential thundering herd)
4. **The Emergency mode triggers** (when would membership fail?)
