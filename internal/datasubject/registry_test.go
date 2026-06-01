package datasubject

import (
	"context"
	"errors"
	"testing"
)

func TestExportFansOutAndIsolatesErrors(t *testing.T) {
	r := NewRegistry()
	r.Register("ok", ExporterFunc(func(_ context.Context, s Subject) (any, error) {
		return map[string]any{"tenant": s.TenantID, "user": s.UserID}, nil
	}), nil)
	r.Register("empty", ExporterFunc(func(_ context.Context, _ Subject) (any, error) {
		return nil, nil // holds nothing for this subject
	}), nil)
	r.Register("boom", ExporterFunc(func(_ context.Context, _ Subject) (any, error) {
		return nil, errors.New("backend down")
	}), nil)

	res := r.Export(context.Background(), Subject{TenantID: "t1", UserID: "u1"})

	if _, ok := res.Namespaces["ok"]; !ok {
		t.Error("expected 'ok' namespace data")
	}
	if _, ok := res.Namespaces["empty"]; ok {
		t.Error("nil export should be omitted, not present")
	}
	if res.Errors["boom"] == "" {
		t.Error("expected a recorded error for 'boom', not a fatal failure")
	}
}

func TestEraseFansOutAndCounts(t *testing.T) {
	r := NewRegistry()
	r.Register("a", nil, EraserFunc(func(_ context.Context, _ Subject) (int, error) { return 3, nil }))
	r.Register("b", nil, EraserFunc(func(_ context.Context, _ Subject) (int, error) { return 0, nil }))
	r.Register("c", nil, EraserFunc(func(_ context.Context, _ Subject) (int, error) {
		return 0, errors.New("cannot reach store")
	}))

	res := r.Erase(context.Background(), Subject{TenantID: "t1", UserID: "u1"})

	if res.Deleted["a"] != 3 {
		t.Errorf("expected 3 deleted from 'a', got %d", res.Deleted["a"])
	}
	if _, ok := res.Deleted["b"]; !ok {
		t.Error("expected 'b' present with 0 deleted")
	}
	if res.Errors["c"] == "" {
		t.Error("expected recorded error for 'c'")
	}
}

func TestNamespacesSorted(t *testing.T) {
	r := NewRegistry()
	r.Register("zeta", ExporterFunc(func(context.Context, Subject) (any, error) { return nil, nil }), nil)
	r.Register("alpha", ExporterFunc(func(context.Context, Subject) (any, error) { return nil, nil }), nil)
	got := r.Namespaces()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Errorf("expected sorted [alpha zeta], got %v", got)
	}
}

func TestEmptyRegistryNoPanic(t *testing.T) {
	r := NewRegistry()
	exp := r.Export(context.Background(), Subject{TenantID: "t1"})
	if len(exp.Namespaces) != 0 {
		t.Error("expected empty export")
	}
	er := r.Erase(context.Background(), Subject{TenantID: "t1"})
	if len(er.Deleted) != 0 {
		t.Error("expected empty erase")
	}
}
