# SessionHost Test Suite

This package provides a comprehensive, reusable test suite for implementations of the `sessions.SessionHost` interface.

The suite treats `SessionHost` as a black box and exercises:

- Per-session messaging semantics (publish/subscribe, ordering, resume, isolation)
- Cleanup semantics
- Optional revocation primitives (precise per-session revocation, epoch bump/get)
- Integration with the `sessions.Manager` and `sessions.Session` wrappers

It is designed to validate both in-memory and distributed implementations (e.g., Redis) without making assumptions about internal details. Where behavior may reasonably differ across implementations (e.g., resuming from a non-existent event ID), tests accept multiple correct outcomes (error vs. timeout with no delivery) rather than over-constraining.

## Usage

Implement a factory that creates a fresh `sessions.SessionHost` for each test and run the suite:

```go
package myhost

import (
    "testing"
    "github.com/ggoodman/mcp-server-go/sessions"
    "github.com/ggoodman/mcp-server-go/sessions/sessionhosttest"
)

func TestMySessionHost(t *testing.T) {
    factory := func(t *testing.T) sessions.SessionHost {
        return NewMySessionHost(/* config */)
    }

    sessionhosttest.RunSessionHostTests(t, factory)
}
```

## Notes

- Revocation tests are skipped automatically if your host returns `sessions.ErrRevocationUnsupported`.
- The suite includes manager-level integration tests that use `sessions.NewManager` and the in-repo `sessions.MemoryJWS` for signed session IDs with issuer binding.
