// internal/engine/poller.go
package engine

import (
	"errors"
	"net"
	"strings"
	"time"
)

// Client abstracts the Modbus read operations needed by the poller.
// Geometry only: no semantics, no scaling.
type Client interface {
	ReadCoils(addr, qty uint16) ([]bool, error)              // FC 1
	ReadDiscreteInputs(addr, qty uint16) ([]bool, error)     // FC 2
	ReadHoldingRegisters(addr, qty uint16) ([]uint16, error) // FC 3
	ReadInputRegisters(addr, qty uint16) ([]uint16, error)   // FC 4
}

// PollerConfig is the minimal runtime config the poller needs.
type PollerConfig struct {
	UnitID   string
	Interval time.Duration
	Reads    []ReadBlock
}

// Poller reads from a field device via a Client.
// It reuses the client while healthy and discards it when the connection is dead.
// No retries are performed inside a poll cycle.
// A future tick may create a new client via factory.
type Poller struct {
	cfg PollerConfig

	client  Client
	factory func() (Client, error)

	// Transport lifetime instrumentation (passive only)
	counters TransportCounters
}

// NewPoller creates a Poller with immutable config.
//   - client is the initial connected client (may be nil)
//   - factory creates a new connected client when the current one is missing/dead
func NewPoller(cfg PollerConfig, client Client, factory func() (Client, error)) (*Poller, error) {
	if cfg.UnitID == "" {
		return nil, errors.New("poller: unit id required")
	}
	if cfg.Interval <= 0 {
		return nil, errors.New("poller: interval must be > 0")
	}
	if len(cfg.Reads) == 0 {
		return nil, errors.New("poller: at least one read block required")
	}

	return &Poller{
		cfg:     cfg,
		client:  client,
		factory: factory,
	}, nil
}

// PollOnce performs exactly one poll cycle.
// All-or-nothing: any failure aborts the cycle and returns a PollResult with Err set.
//
// Connection policy:
//   - Reuse existing client while healthy.
//   - If client is nil, try to create one via factory.
//   - On a dead-connection error, discard client so the next tick can reconnect.
func (p *Poller) PollOnce() PollResult {
	p.counters.RequestsTotal++

	res := PollResult{
		UnitID: p.cfg.UnitID,
		At:     time.Now(),
	}

	if p.client == nil {
		if p.factory == nil {
			res.Err = errors.New("poller: client is nil and no factory provided")
			p.recordFailure(res.Err)
			return res
		}
		c, err := p.factory()
		if err != nil {
			res.Err = err
			p.recordFailure(err)
			return res
		}
		p.client = c
	}

	var blocks []BlockResult

	for _, rb := range p.cfg.Reads {
		switch rb.FC {
		case 1:
			bits, err := p.client.ReadCoils(rb.Address, rb.Quantity)
			if err != nil {
				p.maybeInvalidateClient(err)
				res.Err = err
				p.recordFailure(err)
				return res
			}
			blocks = append(blocks, BlockResult{
				FC: rb.FC, Address: rb.Address, Quantity: rb.Quantity, Bits: bits,
			})

		case 2:
			bits, err := p.client.ReadDiscreteInputs(rb.Address, rb.Quantity)
			if err != nil {
				p.maybeInvalidateClient(err)
				res.Err = err
				p.recordFailure(err)
				return res
			}
			blocks = append(blocks, BlockResult{
				FC: rb.FC, Address: rb.Address, Quantity: rb.Quantity, Bits: bits,
			})

		case 3:
			regs, err := p.client.ReadHoldingRegisters(rb.Address, rb.Quantity)
			if err != nil {
				p.maybeInvalidateClient(err)
				res.Err = err
				p.recordFailure(err)
				return res
			}
			blocks = append(blocks, BlockResult{
				FC: rb.FC, Address: rb.Address, Quantity: rb.Quantity, Registers: regs,
			})

		case 4:
			regs, err := p.client.ReadInputRegisters(rb.Address, rb.Quantity)
			if err != nil {
				p.maybeInvalidateClient(err)
				res.Err = err
				p.recordFailure(err)
				return res
			}
			blocks = append(blocks, BlockResult{
				FC: rb.FC, Address: rb.Address, Quantity: rb.Quantity, Registers: regs,
			})

		default:
			res.Err = errors.New("poller: unsupported function code")
			p.recordFailure(res.Err)
			return res
		}
	}

	res.Blocks = blocks
	p.recordSuccess()
	return res
}

// Counters returns a snapshot copy of the transport counters.
func (p *Poller) Counters() TransportCounters {
	return p.counters
}

// UnitID returns the unit identifier for this poller.
func (p *Poller) UnitID() string {
	return p.cfg.UnitID
}

func (p *Poller) recordSuccess() {
	p.counters.ResponsesValidTotal++
	p.counters.ConsecutiveFailCurr = 0
}

func (p *Poller) recordFailure(err error) {
	if err == nil {
		return
	}

	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		p.counters.TimeoutsTotal++
	} else {
		p.counters.TransportErrorsTotal++
	}

	p.counters.ConsecutiveFailCurr++
	if p.counters.ConsecutiveFailCurr > p.counters.ConsecutiveFailMax {
		p.counters.ConsecutiveFailMax = p.counters.ConsecutiveFailCurr
	}
}

// maybeInvalidateClient discards the current client only when the error indicates
// the underlying TCP connection is dead.
func (p *Poller) maybeInvalidateClient(err error) {
	if err == nil || !isDeadConnErr(err) {
		return
	}
	if c, ok := p.client.(interface{ Close() error }); ok {
		_ = c.Close()
	}
	p.client = nil
}

// isDeadConnErr is a conservative classifier for transport-death errors.
func isDeadConnErr(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return false
	}

	s := strings.ToLower(err.Error())
	return strings.Contains(s, "eof") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "connection aborted") ||
		strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "forcibly closed by the remote host") ||
		strings.Contains(s, "wsasend") ||
		strings.Contains(s, "wsarecv")
}
