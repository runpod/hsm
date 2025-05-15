// Package muid implements a generator for Monotonically Unique IDs (MUIDs).
// MUIDs are 64-bit values inspired by Twitter's Snowflake IDs.
// The default layout is:
//
//	[41 bits timestamp (milliseconds since epoch)] [14 bits machine ID] [9 bits counter]
//
// The bit allocation for timestamp, machine ID, and counter, as well as the epoch,
// can be customized via the Config struct.
// The default epoch starts November 14, 2023 22:13:20 GMT.
package muid

import (
	"crypto/rand"
	"encoding/binary"
	"hash/fnv"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// shardedGenerators holds the pool of generators for sharded access.
type shardedGenerators struct {
	pool []*Generator
	size uint64
	idx  atomic.Uint64
}

var (
	DefaultConfig = sync.OnceValue(func() Config {
		config := Config{
			TimestampBitLen: 40,
			MachineIDBitLen: 14,
			Epoch:           1700000000000,
		}

		// Calculate remaining bits for machine ID after accounting for shard bits
		// Original machineBits was 14 (64 - 41 timestamp - 9 counter)
		machineIdMask := uint64((1 << config.MachineIDBitLen) - 1)

		hostname, err := os.Hostname()
		var machineID uint64
		if err != nil || hostname == "" {
			// Fallback: random value masked to machineBitLen
			var b [8]byte          // Read enough bytes for uint64
			_, _ = rand.Read(b[:]) // best effort
			machineID = binary.BigEndian.Uint64(b[:]) & machineIdMask
		} else {
			hash := fnv.New64a()
			hash.Write([]byte(hostname))
			machineID = hash.Sum64() & machineIdMask
		}
		config.MachineID = machineID
		return config
	})

	// Global instance of sharded generators, initialized once.
	defaultShards = sync.OnceValue(func() *shardedGenerators {
		numCPU := max(runtime.NumCPU(), 1)
		shardBits := 0
		if numCPU > 1 {
			shardBits = min(int(math.Ceil(math.Log2(float64(numCPU)))), 5)
		}

		// Get base config potentially containing the calculated MachineID
		defaultConfig := DefaultConfig()

		// Create a config template to pass to each generator constructor
		// This ensures consistent Epoch, BitLen settings across shards.
		cfgTemplate := Config{
			MachineID:       defaultConfig.MachineID, // Use the already calculated and masked ID
			TimestampBitLen: defaultConfig.TimestampBitLen,
			MachineIDBitLen: defaultConfig.MachineIDBitLen,
			Epoch:           defaultConfig.Epoch,
		}

		pool := make([]*Generator, 1<<shardBits)
		for i := 0; i < 1<<shardBits; i++ {
			// Each generator uses the correct config template, its unique shard index (i),
			// and the calculated shardBitLen.
			pool[i] = NewGenerator(cfgTemplate, uint64(i), shardBits)
		}
		return &shardedGenerators{
			pool: pool,
			size: uint64(1 << shardBits),
		}
	})

	// Keep defaultConfig for NewGenerator defaults
	defaultConfig = DefaultConfig()
	shards        = defaultShards()
)

type Config struct {
	MachineID       uint64
	TimestampBitLen int
	MachineIDBitLen int
	Epoch           int64
}

// MUID represents a Monotonically Unique ID.
type MUID uint64

// String returns the base32 encoded string representation of the MUID,
// zero-padded to 13 characters.
func (m MUID) String() string {
	return strconv.FormatUint(uint64(m), 32)
}

// Generator is responsible for generating MUIDs.
// It maintains the last used timestamp and counter atomically.
type Generator struct {
	machineID         uint64
	timestampBitLen   int
	counterBitLen     int
	machineIdBitLen   int
	timestampBitShift int
	counterBitMask    uint64
	epoch             int64
	// state packs the last timestamp (upper bits) and counter (lower bits).
	// The number of bits used depends on the configured counterBitLen.
	state           atomic.Uint64
	shardIndex      uint64
	shardBitLen     int
	machineIDShift  int
	shardIndexShift int
}

// NewGenerator creates a new MUID generator based on the provided configuration.
// It calculates the necessary bit shifts and masks based on the config.
// Missing or zero values in the config will be replaced by default values.
// The provided MachineID in the config will be masked to fit the calculated
// machine bit length (64 - timestampBitLen - counterBitLen - shardBitLen).
func NewGenerator(config Config, shardIndex uint64, shardBitLen int) *Generator {
	generator := &Generator{
		machineID:       config.MachineID,
		timestampBitLen: config.TimestampBitLen,
		machineIdBitLen: config.MachineIDBitLen,
		epoch:           config.Epoch,
		shardIndex:      shardIndex,
		shardBitLen:     shardBitLen,
	}
	// apply defaults if not set
	if generator.machineIdBitLen <= 0 {
		generator.machineIdBitLen = defaultConfig.MachineIDBitLen
	}
	if generator.epoch <= 0 {
		generator.epoch = defaultConfig.Epoch // Use package-level default
	}
	if generator.timestampBitLen <= 0 {
		generator.timestampBitLen = defaultConfig.TimestampBitLen // Use package-level default
	}

	// Calculate actual machine bit length and shifts considering shard bits
	generator.counterBitLen = 64 - generator.timestampBitLen - generator.machineIdBitLen - generator.shardBitLen

	generator.timestampBitShift = generator.machineIdBitLen + generator.shardBitLen + generator.counterBitLen
	generator.machineIDShift = generator.shardBitLen + generator.counterBitLen
	generator.shardIndexShift = generator.counterBitLen

	generator.counterBitMask = (1 << generator.counterBitLen) - 1

	if generator.machineID <= 0 {
		generator.machineID = defaultConfig.MachineID // Use package-level default if not provided
	}
	// Mask the machine ID to fit the adjusted machineBitLen
	generator.machineID = generator.machineID & ((1 << generator.machineIdBitLen) - 1)

	// Mask the shard index to fit the shardBitLen
	generator.shardIndex = generator.shardIndex & ((1 << generator.shardBitLen) - 1)

	generator.state.Store(1)
	return generator
}

// ID generates a new MUID.
// It is thread-safe and handles clock regressions and counter overflows.
// If the counter overflows within a millisecond, it increments the timestamp
// virtually to ensure monotonicity.
func (g *Generator) ID() MUID {
	for {
		now := uint64(time.Now().UnixMilli() - g.epoch)

		previousState := g.state.Load()
		// Extract last timestamp and counter from the packed state.
		lastTimestamp := previousState >> g.counterBitLen
		counter := (previousState & g.counterBitMask)

		// Protect against clock moving backwards.
		if now < lastTimestamp {
			now = lastTimestamp // Use the last known timestamp if clock went backwards
		}

		if now == lastTimestamp {
			// Same millisecond as the last ID generation.
			if counter >= g.counterBitMask {
				// Counter overflowed, increment the timestamp virtually.
				now++
				counter = 1 // Reset counter for the new virtual millisecond
			} else {
				// Increment counter within the same millisecond.
				counter++
			}
		} else {
			// New millisecond, reset the counter.
			counter = 1
		}

		// Pack the new timestamp and counter into the state.
		newState := (now << g.counterBitLen) | counter
		// Atomically update the state using Compare-and-Swap.
		if g.state.CompareAndSwap(previousState, newState) {
			// Construct the final MUID.
			// Structure: [Timestamp][MachineID][ShardIndex][Counter]
			muid := (now << g.timestampBitShift) |
				(g.machineID << g.machineIDShift) |
				(g.shardIndex << g.shardIndexShift) |
				counter
			return MUID(muid)
		}
		// Retry if the state was modified by another goroutine (CAS failure).
	}
}

// Make generates a new MUID using the default sharded generators.
// It distributes load across generators for better parallel performance.
func Make() MUID {
	// Atomically get the next index in a round-robin fashion.
	idx := shards.idx.Add(1) % shards.size
	return shards.pool[idx].ID()
}
