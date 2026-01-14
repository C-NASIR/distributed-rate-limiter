// Package core provides batch planning.
package core

import "sync"

// BatchPlanner plans batch limiter calls.
type BatchPlanner struct {
	indexPool *IndexPool
	keyPool   *KeyPool
	costPool  *CostPool
}

// IndexPool pools int slices.
type IndexPool struct {
	pool sync.Pool
}

// KeyPool pools key slices.
type KeyPool struct {
	pool sync.Pool
}

// CostPool pools cost slices.
type CostPool struct {
	pool sync.Pool
}

// NewBatchPlanner constructs a BatchPlanner with pools.
func NewBatchPlanner(indexPool *IndexPool, keyPool *KeyPool, costPool *CostPool) *BatchPlanner {
	if indexPool == nil {
		indexPool = NewIndexPool()
	}
	if keyPool == nil {
		keyPool = NewKeyPool()
	}
	if costPool == nil {
		costPool = NewCostPool()
	}
	return &BatchPlanner{
		indexPool: indexPool,
		keyPool:   keyPool,
		costPool:  costPool,
	}
}

// BatchPlan captures grouped batch work.
type BatchPlan struct {
	Groups []BatchGroup
}

// BatchGroup is a group of requests for a tenant/resource.
type BatchGroup struct {
	TenantID string
	Resource string
	Indexes  []int
	Keys     [][]byte
	Costs    []int64
}

// NewIndexPool constructs an IndexPool.
func NewIndexPool() *IndexPool {
	return &IndexPool{}
}

// NewKeyPool constructs a KeyPool.
func NewKeyPool() *KeyPool {
	return &KeyPool{}
}

// NewCostPool constructs a CostPool.
func NewCostPool() *CostPool {
	return &CostPool{}
}

// Get returns an empty index slice.
func (p *IndexPool) Get() []int {
	if p == nil {
		return nil
	}
	if value := p.pool.Get(); value != nil {
		return value.([]int)[:0]
	}
	return nil
}

// Put returns an index slice to the pool.
func (p *IndexPool) Put(indexes []int) {
	if p == nil || indexes == nil {
		return
	}
	p.pool.Put(indexes[:0])
}

// Get returns an empty key slice.
func (p *KeyPool) Get() [][]byte {
	if p == nil {
		return nil
	}
	if value := p.pool.Get(); value != nil {
		return value.([][]byte)[:0]
	}
	return nil
}

// Put returns a key slice to the pool.
func (p *KeyPool) Put(keys [][]byte) {
	if p == nil || keys == nil {
		return
	}
	p.pool.Put(keys[:0])
}

// Get returns an empty cost slice.
func (p *CostPool) Get() []int64 {
	if p == nil {
		return nil
	}
	if value := p.pool.Get(); value != nil {
		return value.([]int64)[:0]
	}
	return nil
}

// Put returns a cost slice to the pool.
func (p *CostPool) Put(costs []int64) {
	if p == nil || costs == nil {
		return
	}
	p.pool.Put(costs[:0])
}

// Plan groups requests by tenant/resource.
func (bp *BatchPlanner) Plan(reqs []*CheckLimitRequest) *BatchPlan {
	plan := &BatchPlan{}
	if bp == nil || len(reqs) == 0 {
		return plan
	}
	if bp.indexPool == nil {
		bp.indexPool = NewIndexPool()
	}
	if bp.keyPool == nil {
		bp.keyPool = NewKeyPool()
	}
	if bp.costPool == nil {
		bp.costPool = NewCostPool()
	}
	groupIndex := make(map[string]int)
	for i, req := range reqs {
		if req == nil {
			continue
		}
		key := limiterPoolKey(req.TenantID, req.Resource)
		idx, ok := groupIndex[key]
		if !ok {
			idx = len(plan.Groups)
			groupIndex[key] = idx
			plan.Groups = append(plan.Groups, BatchGroup{
				TenantID: req.TenantID,
				Resource: req.Resource,
				Indexes:  bp.indexPool.Get(),
				Keys:     bp.keyPool.Get(),
				Costs:    bp.costPool.Get(),
			})
		}
		group := &plan.Groups[idx]
		group.Indexes = append(group.Indexes, i)
		group.Keys = append(group.Keys, nil)
		group.Costs = append(group.Costs, req.Cost)
	}
	return plan
}

// Release returns plan resources to pools.
func (bp *BatchPlanner) Release(plan *BatchPlan) {
	if bp == nil || plan == nil {
		return
	}
	for i := range plan.Groups {
		group := &plan.Groups[i]
		bp.indexPool.Put(group.Indexes)
		bp.keyPool.Put(group.Keys)
		bp.costPool.Put(group.Costs)
		group.Indexes = nil
		group.Keys = nil
		group.Costs = nil
	}
	plan.Groups = nil
}
