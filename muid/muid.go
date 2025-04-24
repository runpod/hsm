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
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	DefaultConfig = sync.OnceValue(func() Config {
		config := Config{
			TimestampBitLen: 41,
			CounterBitLen:   9,
			Epoch:           1700000000000,
		}
		machineBits := 14
		hostname, err := os.Hostname()
		var machineID uint64
		if err != nil || hostname == "" {
			// Fallback: random 14-bit value
			var b [2]byte
			_, _ = rand.Read(b[:]) // best effort, safe fallback
			// Mask to 14 bits
			machineID = uint64(binary.BigEndian.Uint16(b[:])) & ((1 << machineBits) - 1)
		} else {
			hash := fnv.New64a()
			hash.Write([]byte(hostname))
			machineID = hash.Sum64() & ((1 << machineBits) - 1)
		}
		config.MachineID = machineID
		return config
	})

	// DefaultGenerator provides a singleton Generator instance using the default MachineID.
	DefaultGenerator = sync.OnceValue(func() *Generator {
		return NewGenerator(DefaultConfig())
	})

	defaultGenerator = DefaultGenerator()
	defaultConfig    = DefaultConfig()
)

type Config struct {
	MachineID       uint64
	TimestampBitLen int
	CounterBitLen   int
	Epoch           int64
}

// MUID represents a Monotonically Unique ID.
type MUID uint64

// String returns the base32 encoded string representation of the MUID,
// zero-padded to 13 characters.
func (m MUID) String() string {
	var buf [13]byte
	str := strconv.FormatUint(uint64(m), 32)
	pad := 13 - len(str)

	for i := range pad {
		buf[i] = '0'
	}
	copy(buf[pad:], str)
	return string(buf[:])
}

// Generator is responsible for generating MUIDs.
// It maintains the last used timestamp and counter atomically.
type Generator struct {
	machineID         uint64
	timestampBitLen   int
	counterBitLen     int
	machineBitLen     int
	timestampBitShift int
	counterBitMask    uint64
	epoch             int64
	// state packs the last timestamp (upper bits) and counter (lower bits).
	// The number of bits used depends on the configured counterBitLen.
	state atomic.Uint64
}

// NewGenerator creates a new MUID generator based on the provided configuration.
// It calculates the necessary bit shifts and masks based on the config.
// Missing or zero values in the config will be replaced by default values.
// The provided MachineID in the config will be masked to fit the calculated
// machine bit length (64 - timestampBitLen - counterBitLen).
func NewGenerator(config Config) *Generator {
	generator := &Generator{
		machineID:       config.MachineID,
		timestampBitLen: config.TimestampBitLen,
		counterBitLen:   config.CounterBitLen,
		epoch:           config.Epoch,
	}
	// apply defaults if not set
	if generator.counterBitLen <= 0 {
		generator.counterBitLen = defaultConfig.CounterBitLen
	}
	if generator.epoch <= 0 {
		generator.epoch = defaultConfig.Epoch
	}
	if generator.timestampBitLen <= 0 {
		generator.timestampBitLen = defaultConfig.TimestampBitLen
	}
	if generator.epoch <= 0 {
		generator.epoch = defaultConfig.Epoch
	}
	generator.machineBitLen = 64 - generator.timestampBitLen - generator.counterBitLen
	generator.timestampBitShift = generator.machineBitLen + generator.counterBitLen
	generator.counterBitMask = (1 << generator.counterBitLen) - 1
	if generator.machineID <= 0 {
		generator.machineID = defaultConfig.MachineID
	}
	generator.machineID = generator.machineID & ((1 << generator.machineBitLen) - 1)
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
		counter := previousState & g.counterBitMask

		// Protect against clock moving backwards.
		if now < lastTimestamp {
			now = lastTimestamp // Use the last known timestamp if clock went backwards
		}

		if now == lastTimestamp {
			// Same millisecond as the last ID generation.
			if counter >= g.counterBitMask {
				// Counter overflowed, increment the timestamp virtually.
				now++
				counter = 0 // Reset counter for the new virtual millisecond
			} else {
				// Increment counter within the same millisecond.
				counter++
			}
		} else {
			// New millisecond, reset the counter.
			counter = 0
		}

		// Pack the new timestamp and counter into the state.
		newState := (now << g.counterBitLen) | counter
		// Atomically update the state using Compare-and-Swap.
		if g.state.CompareAndSwap(previousState, newState) {
			// Construct the final MUID.
			return MUID((now << g.timestampBitShift) | (g.machineID << g.counterBitLen) | counter)
		}
		// Retry if the state was modified by another goroutine (CAS failure).
	}
}

// Make generates a new MUID using the default generator.
func Make() MUID {
	return defaultGenerator.ID()
}
