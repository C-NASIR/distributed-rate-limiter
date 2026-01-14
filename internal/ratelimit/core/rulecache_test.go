package core

import (
	"sync"
	"testing"
	"time"
)

func TestRuleCache_ReplaceAll_Get_List(t *testing.T) {
	t.Parallel()

	cache := NewRuleCache()
	rules := []*Rule{
		{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1},
		{TenantID: "tenant-a", Resource: "resource-2", Limit: 20, Version: 2},
		{TenantID: "tenant-b", Resource: "resource-1", Limit: 30, Version: 3},
	}
	cache.ReplaceAll(rules)

	for _, rule := range rules {
		got, ok := cache.Get(rule.TenantID, rule.Resource)
		if !ok {
			t.Fatalf("expected rule for %s/%s", rule.TenantID, rule.Resource)
		}
		if got.Version != rule.Version {
			t.Fatalf("expected version %d got %d", rule.Version, got.Version)
		}
	}

	list := cache.List("tenant-a")
	if len(list) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(list))
	}
	seen := make(map[string]int64)
	for _, rule := range list {
		seen[rule.Resource] = rule.Version
	}
	if seen["resource-1"] != 1 || seen["resource-2"] != 2 {
		t.Fatalf("unexpected list contents: %#v", seen)
	}
}

func TestRuleCache_UpsertIfNewer(t *testing.T) {
	t.Parallel()

	cache := NewRuleCache()
	cache.UpsertIfNewer(&Rule{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1})
	cache.UpsertIfNewer(&Rule{TenantID: "tenant-a", Resource: "resource-1", Limit: 20, Version: 1})

	got, ok := cache.Get("tenant-a", "resource-1")
	if !ok {
		t.Fatalf("expected rule to exist")
	}
	if got.Limit != 10 || got.Version != 1 {
		t.Fatalf("expected version 1 limit 10, got version %d limit %d", got.Version, got.Limit)
	}

	cache.UpsertIfNewer(&Rule{TenantID: "tenant-a", Resource: "resource-1", Limit: 30, Version: 2})
	got, ok = cache.Get("tenant-a", "resource-1")
	if !ok {
		t.Fatalf("expected rule to exist")
	}
	if got.Limit != 30 || got.Version != 2 {
		t.Fatalf("expected version 2 limit 30, got version %d limit %d", got.Version, got.Limit)
	}
}

func TestRuleCache_DeleteIfOlderOrEqual(t *testing.T) {
	t.Parallel()

	cache := NewRuleCache()
	cache.UpsertIfNewer(&Rule{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 5})

	cache.DeleteIfOlderOrEqual("tenant-a", "resource-1", 4)
	if _, ok := cache.Get("tenant-a", "resource-1"); !ok {
		t.Fatalf("expected rule to remain after older delete")
	}

	cache.DeleteIfOlderOrEqual("tenant-a", "resource-1", 5)
	if _, ok := cache.Get("tenant-a", "resource-1"); ok {
		t.Fatalf("expected rule to be deleted at version 5")
	}

	cache.UpsertIfNewer(&Rule{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 5})
	cache.DeleteIfOlderOrEqual("tenant-a", "resource-1", 10)
	if _, ok := cache.Get("tenant-a", "resource-1"); ok {
		t.Fatalf("expected rule to be deleted at version 10")
	}
}

func TestRuleCache_ConcurrentReadsAndWrites_NoRace(t *testing.T) {
	t.Parallel()

	cache := NewRuleCache()
	cache.ReplaceAll([]*Rule{
		{TenantID: "tenant-a", Resource: "resource-1", Limit: 10, Version: 1},
		{TenantID: "tenant-b", Resource: "resource-2", Limit: 20, Version: 1},
	})

	tenants := []string{"tenant-a", "tenant-b"}
	resources := []string{"resource-1", "resource-2"}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tenant := tenants[idx%len(tenants)]
				resource := resources[idx%len(resources)]
				cache.Get(tenant, resource)
				cache.List(tenant)
			}
		}(i)
	}

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			version := int64(1)
			for {
				select {
				case <-stop:
					return
				default:
				}
				version++
				tenant := tenants[int(version)%len(tenants)]
				resource := resources[(idx+int(version))%len(resources)]
				cache.UpsertIfNewer(&Rule{TenantID: tenant, Resource: resource, Limit: version, Version: version})
				cache.DeleteIfOlderOrEqual(tenant, resource, version-1)
			}
		}(i)
	}

	<-time.After(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}
