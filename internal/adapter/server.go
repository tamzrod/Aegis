// internal/adapter/server.go
// Re-exports Server types from internal/memory.
package adapter

import "github.com/tamzrod/Aegis/internal/memory"

type Server = memory.Server

var NewServer = memory.NewServer
var NewServerWithListener = memory.NewServerWithListener
