package muid_test

import (
	"strconv"
	"testing"

	"github.com/godruoyi/go-snowflake"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
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

func TestMUIDStringLength(t *testing.T) {
	const total = 1_000_000
	length := len(muid.Make().String())
	for i := 0; i < total; i++ {
		muid := muid.Make()
		if len(muid.String()) != length {
			t.Fatalf("muid string length is not %d: %d", length, len(muid.String()))
		}
	}
}

func BenchmarkIds(b *testing.B) {
	b.Run("uuid", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			uuid.NewV7()
		}
	})
	b.Run("uuid_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				uuid.NewV7()
			}
		})
	})
	b.Run("uuid_string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			uuid, _ := uuid.NewV7()
			_ = uuid.String()
		}
	})
	b.Run("uuid_string_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				uuid, _ := uuid.NewV7()
				_ = uuid.String()
			}
		})
	})
	b.Run("snowflake", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			snowflake.ID()
		}
	})
	b.Run("snowflake_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				snowflake.ID()
			}
		})
	})
	b.Run("snowflake_string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			id := snowflake.ID()
			_ = strconv.FormatUint(id, 10)
		}
	})
	b.Run("snowflake_string_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				id := snowflake.ID()
				_ = strconv.FormatUint(id, 10)
			}
		})
	})
	b.Run("ulid", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ulid.Make()
		}
	})
	b.Run("ulid_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				ulid.Make()
			}
		})
	})
	b.Run("ulid_string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			id := ulid.Make()
			_ = id.String()
		}
	})
	b.Run("ulid_string_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				id := ulid.Make()
				_ = id.String()
			}
		})
	})
	b.Run("muid", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			muid.Make()
		}
	})
	b.Run("muid_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				muid.Make()
			}
		})
	})
	b.Run("muid_string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			id := muid.Make()
			_ = id.String()
		}
	})
	b.Run("muid_string_parallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				id := muid.Make()
				_ = id.String()
			}
		})
	})
}
