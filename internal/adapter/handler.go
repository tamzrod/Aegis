// internal/adapter/handler.go
// Re-exports handler types from internal/memory.
package adapter

import "github.com/tamzrod/Aegis/internal/memory"

type Enforcer = memory.Enforcer

var HandleConn = memory.HandleConn
