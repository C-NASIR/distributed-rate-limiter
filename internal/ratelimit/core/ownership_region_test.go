package core

import (
	"context"
	"fmt"
	"hash/fnv"
	"testing"
)

func TestRendezvousOwnership_RegionScoped(t *testing.T) {
	t.Parallel()

	instances := []InstanceInfo{
		{ID: "a1", Region: "region-a", Weight: 1},
		{ID: "a2", Region: "region-a", Weight: 1},
		{ID: "b1", Region: "region-b", Weight: 1},
		{ID: "b2", Region: "region-b", Weight: 1},
	}
	membership := NewStaticMembership("a1", "region-a", instances)
	owner := NewRendezvousOwnership(membership, "region-a", false, true)

	key := findKeyWithOwners(t, instances, "region-a", "a1", "region-b")
	if !owner.IsOwner(context.Background(), key) {
		t.Fatalf("expected region scoped ownership to stay in region")
	}
}

func TestRendezvousOwnership_GlobalEnabled(t *testing.T) {
	t.Parallel()

	instances := []InstanceInfo{
		{ID: "a1", Region: "region-a", Weight: 1},
		{ID: "a2", Region: "region-a", Weight: 1},
		{ID: "b1", Region: "region-b", Weight: 1},
		{ID: "b2", Region: "region-b", Weight: 1},
	}
	membership := NewStaticMembership("a1", "region-a", instances)
	owner := NewRendezvousOwnership(membership, "region-a", true, true)

	key := findKeyWithGlobalOwner(t, instances, "region-b")
	if owner.IsOwner(context.Background(), key) {
		t.Fatalf("expected global ownership to choose other region")
	}
}

func TestRendezvousOwnership_GlobalFallbackWhenRegionEmpty(t *testing.T) {
	t.Parallel()

	instances := []InstanceInfo{
		{ID: "a1", Region: "region-a", Weight: 1},
		{ID: "b1", Region: "region-b", Weight: 1},
	}
	membership := NewStaticMembership("a1", "region-a", instances)
	owner := NewRendezvousOwnership(membership, "region-c", false, true)

	key := []byte("fallback-key")
	first := owner.IsOwner(context.Background(), key)
	second := owner.IsOwner(context.Background(), key)
	if first != second {
		t.Fatalf("expected stable ownership when region empty")
	}
}

func findKeyWithOwners(t *testing.T, instances []InstanceInfo, region string, expectedOwner string, otherRegion string) []byte {
	for i := 0; i < 10000; i++ {
		candidate := []byte(fmt.Sprintf("key-%d", i))
		globalOwner := rendezvousOwner(instances, candidate)
		if globalOwner == "" || instanceRegion(instances, globalOwner) != otherRegion {
			continue
		}
		regionOwner := rendezvousOwner(filterRegion(instances, region), candidate)
		if regionOwner == expectedOwner {
			return candidate
		}
	}
	t.Fatalf("failed to locate key with expected owners")
	return nil
}

func findKeyWithGlobalOwner(t *testing.T, instances []InstanceInfo, region string) []byte {
	for i := 0; i < 10000; i++ {
		candidate := []byte(fmt.Sprintf("key-%d", i))
		owner := rendezvousOwner(instances, candidate)
		if owner != "" && instanceRegion(instances, owner) == region {
			return candidate
		}
	}
	t.Fatalf("failed to locate key with global owner in region")
	return nil
}

func filterRegion(instances []InstanceInfo, region string) []InstanceInfo {
	filtered := make([]InstanceInfo, 0, len(instances))
	for _, instance := range instances {
		if instance.Region == region {
			filtered = append(filtered, instance)
		}
	}
	return filtered
}

func instanceRegion(instances []InstanceInfo, id string) string {
	for _, instance := range instances {
		if instance.ID == id {
			return instance.Region
		}
	}
	return ""
}

func rendezvousOwner(instances []InstanceInfo, key []byte) string {
	var bestID string
	var bestScore uint64
	for i, instance := range instances {
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(instance.ID))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(key)
		score := hasher.Sum64()
		weight := instance.Weight
		if weight < 1 {
			weight = 1
		}
		score *= uint64(weight)
		if i == 0 || score > bestScore {
			bestScore = score
			bestID = instance.ID
		}
	}
	return bestID
}
