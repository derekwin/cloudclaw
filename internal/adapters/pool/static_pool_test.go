package pool

import "testing"

func TestNewStaticNormalizesAndDeduplicatesIDs(t *testing.T) {
	p, err := NewStatic([]string{" container-1 ", "", "container-2", "container-1"})
	if err != nil {
		t.Fatalf("NewStatic returned error: %v", err)
	}
	if len(p.IDs) != 2 {
		t.Fatalf("expected 2 unique ids, got %d (%v)", len(p.IDs), p.IDs)
	}
	if p.IDs[0] != "container-1" || p.IDs[1] != "container-2" {
		t.Fatalf("unexpected ids: %v", p.IDs)
	}
}

func TestNewStaticRejectsOnlyBlankIDs(t *testing.T) {
	if _, err := NewStatic([]string{" ", "\n"}); err == nil {
		t.Fatal("expected error for blank ids")
	}
}
