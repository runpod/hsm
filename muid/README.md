# MUID (Micro Universal ID) Package

This package provides a generator for **M**icro **U**niversal **ID**s (MUIDs). MUIDs are 64-bit unique identifiers inspired by Twitter's Snowflake IDs. They are roughly time-sortable and suitable for use as primary keys in distributed systems.

## Features

- **Unique & Sortable:** Generates unique IDs that are approximately ordered by time.
- **High Performance:** Optimized for generating large numbers of IDs quickly.
- **Concurrency Safe:** Safe for use across multiple goroutines.
- **Configurable:** Allows customization of bit lengths for timestamp, machine ID, and counter, as well as the epoch and machine ID itself via a `Config` struct.

## ID Structure (Default)

Each 64-bit MUID is composed of:

- **Timestamp (41 bits):** Milliseconds since a custom epoch (`1700000000000` - Nov 14, 2023 22:13:20 GMT), allowing for ~69 years of IDs.
- **Machine ID (14 bits):** Identifier for the machine generating the ID, allowing for 16,384 unique machines.
- **Counter (9 bits):** Sequence number within the same millisecond on the same machine, allowing for 512 unique IDs per millisecond per machine.

_Note: The bit allocation for timestamp, machine ID, and counter can be customized._

## Usage

### Default Generator

The simplest way to generate an ID is to use the default generator. It uses the default configuration (41 bits timestamp, 14 bits machine ID, 9 bits counter) and automatically determines a machine ID based on the hostname (or a random value if the hostname is unavailable).

```go
import (
	"fmt"
	"github.com/runpod/hsm/muid"
)

func main() {
	// Generate a new MUID using the default generator
	id := muid.Make()

	fmt.Printf("Generated MUID: %s\n", id) // Outputs the base32 representation
	fmt.Printf("Generated MUID (uint64): %d\n", uint64(id))
}
```

### Custom Generator

You can customize the generator's behavior by providing a `Config` struct. This allows you to specify the bit lengths for the timestamp, machine ID, and counter, a custom epoch, and a specific machine ID.

```go
import (
	"fmt"
	"github.com/runpod/hsm/muid"
	"time"
)

func main() {
	// Define a custom configuration
	config := muid.Config{
		MachineID:       123,              // Assign a specific machine ID
		TimestampBitLen: 42,              // Use 42 bits for timestamp
		CounterBitLen:   10,              // Use 10 bits for counter (machine ID will be 12 bits)
		Epoch:           time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), // Custom epoch
	}

	// Create a generator with the custom configuration
	// The provided MachineID (123) will be masked to fit the calculated machine bit length (64 - 42 - 10 = 12 bits).
	gen := muid.NewGenerator(config)

	// Generate an ID using the custom generator
	id := gen.ID()

	fmt.Printf("Generated MUID with custom config: %s\n", id)
}

```

## Notes

- The default machine ID generation uses a hash of the hostname masked to the available machine ID bits (14 by default).
- If a custom `MachineID` is provided in the `Config`, it will be masked to fit the calculated machine bit length (64 - TimestampBitLen - CounterBitLen).
- The generator handles counter rollover within the same millisecond by incrementing the timestamp component, ensuring uniqueness even under high burst load.
- The implementation guarantees monotonically increasing timestamps. Even if the system clock goes backward, the generator will continue issuing IDs with timestamps based on the last known highest time, ensuring IDs remain sortable by time.
