package config

import "os"

// Windows does not support fsync on directory handles opened through os.Open.
// File contents are synced before publication; no Unix durability is promised.
func syncFragmentDir(*os.Root) error { return nil }
