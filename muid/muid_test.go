package muid_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/runpod/hsm/muid"
)

func TestMUID(t *testing.T) {
	const total = 1_000_000
	muids := make(map[muid.MUID]bool)
	ch := make(chan muid.MUID, total)
	for i := 0; i < total; i++ {
		go func() {
			muid := muid.Make()
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

func BenchmarkMuid(b *testing.B) {
	b.Run("muid", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			muid.Make()
		}
	})
	b.Run("uuid", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			uuid.NewV7()
		}
	})

	b.Run("muid_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				muid.Make()
			}
		})
	})

	b.Run("uuid_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				uuid.NewV7()
			}
		})
	})
	b.Run("muid_string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = muid.Make().String()
		}
	})
	b.Run("uuid_string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			uuid, err := uuid.NewV7()
			if err != nil {
				b.Fatal(err)
			}
			_ = uuid.String()
		}
	})
}
