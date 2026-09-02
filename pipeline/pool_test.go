package pipeline

import "testing"

func TestPoolReusesReleasedValues(t *testing.T) {
	if poolDebug {
		t.Skip("the debug pool poisons rather than reuses")
	}
	pool := newPayloadPool()
	first := pool.Get()
	value := first.Value()
	value.n = 42
	first.Release()

	second := pool.Get()
	if second.Value() != value {
		t.Fatal("Get did not reuse the released value")
	}
	if second.Value().n != 0 {
		t.Fatalf("value was not reset: %v", second.Value().n)
	}
	second.Release()
}

func TestPoolCountsReferences(t *testing.T) {
	pool := newPayloadPool()
	first := pool.Get()
	if pool.Outstanding() != 1 {
		t.Fatalf("outstanding = %v, want 1", pool.Outstanding())
	}
	clone := first.Clone()
	first.Release()
	if pool.Outstanding() != 1 {
		t.Fatalf("outstanding = %v after releasing one of two references, want 1", pool.Outstanding())
	}
	if clone.Value() == nil {
		t.Fatal("the clone lost its value")
	}
	clone.Release()
	if pool.Outstanding() != 0 {
		t.Fatalf("outstanding = %v after releasing both references, want 0", pool.Outstanding())
	}
}

func TestPoolPanicsOnDoubleRelease(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("releasing twice did not panic")
		}
	}()
	p := newPayloadPool().Get()
	p.Release()
	p.Release()
}

func TestPoolPanicsOnUseAfterRelease(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("using a released packet did not panic")
		}
	}()
	p := newPayloadPool().Get()
	p.Release()
	p.Value()
}

func TestPoolCloneAfterReleasePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("cloning a released packet did not panic")
		}
	}()
	p := newPayloadPool().Get()
	p.Release()
	p.Clone()
}
