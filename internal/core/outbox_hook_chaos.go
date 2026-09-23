//go:build chaos

package core

import "os"

// The chaos binary exits after publishing and before committing PostgreSQL.
func afterOutboxPublish() { os.Exit(77) }
