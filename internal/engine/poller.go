// internal/engine/poller.go
// Re-exports Poller types from internal/puller.
package engine

import "github.com/tamzrod/Aegis/internal/puller"

type Client = puller.Client
type Poller = puller.Poller
type PollerConfig = puller.PollerConfig

var NewPoller = puller.NewPoller
