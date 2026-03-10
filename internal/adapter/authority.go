// internal/adapter/authority.go
// Re-exports authority types from internal/memory.
package adapter

import "github.com/tamzrod/Aegis/internal/memory"

type BlockHealthReader = memory.BlockHealthReader
type AuthorityRegistry = memory.AuthorityRegistry

var BuildAuthorityRegistry = memory.BuildAuthorityRegistry
