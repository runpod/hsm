package muid

import (
	"testing"
)

func TestMUID(t *testing.T) {
	const total = 1_000_000
	muids := make(map[MUID]bool)
	ch := make(chan MUID, total)
	for i := 0; i < total; i++ {
		go func() {
			muid := Make()
			ch <- muid
		}()
	}
	for i := range total {
		muid := <-ch
		if muids[muid] {
			t.Fatalf("collision: %d after %d", muid, i)
		}
		muids[muid] = true
	}
}

func TestMUIDStringLength(t *testing.T) {
	const total = 1_000_000
	length := len(Make().String())
	for i := 0; i < total; i++ {
		muid := Make()
		if len(muid.String()) != length {
			t.Fatalf("muid string length is not %d: %d", length, len(muid.String()))
		}
	}
}

func BenchmarkMUID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Make()
	}
}
