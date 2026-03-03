// internal/engine/poller.go
package engine

import (
	"errors"
	"fmt"
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
// Each read block carries its own interval; there is no global interval.
type PollerConfig struct {
	UnitID string
	Reads  []ReadBlock
}

// Poller reads from a field device via a Client.
// It maintains an independent schedule per read block so that fast-moving
// and slow-moving data can be read at different cadences without spawning
// a goroutine per block.
// It reuses the client while healthy and discards it when the connection is dead.
// No retries are performed inside a poll cycle.
// A future tick may create a new client via factory.
type Poller struct {
	cfg PollerConfig

	client  Client
	factory func() (Client, error)

	// nextExec[i] is the earliest time at which reads[i] may be executed.
	// A zero value means the read is immediately due.
	nextExec []time.Time

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
	if len(cfg.Reads) == 0 {
		return nil, errors.New("poller: at least one read block required")
	}
	for i, r := range cfg.Reads {
		if r.Interval <= 0 {
			return nil, fmt.Errorf("poller: reads[%d]: interval must be > 0", i)
		}
	}

	return &Poller{
		cfg:      cfg,
		client:   client,
		factory:  factory,
		nextExec: make([]time.Time, len(cfg.Reads)), // zero → all reads due immediately
	}, nil
}

// PollOnce performs one scheduling tick at the current wall time.
// It executes every read block that is due and skips blocks that are not yet due.
// If no blocks are due, it returns a successful result with an empty Blocks slice.
//
// All-or-nothing within the due set: any failure aborts the remaining due reads
// and returns a PollResult with Err set.  nextExec is only advanced for reads
// that are executed in a fully-successful cycle.
//
// Connection policy:
//   - Reuse existing client while healthy.
//   - If client is nil and at least one block is due, try to create one via factory.
//   - On a dead-connection error, discard client so the next tick can reconnect.
func (p *Poller) PollOnce() PollResult {
	return p.pollAt(time.Now())
}

// pollAt is the internal implementation of PollOnce with an explicit clock value.
// Using an explicit time allows the Run loop to use the ticker's channel time,
// which avoids drift accumulation.
func (p *Poller) pollAt(now time.Time) PollResult {
	p.counters.RequestsTotal++

	res := PollResult{
		UnitID: p.cfg.UnitID,
		At:     now,
	}

	// Determine which read blocks are due at this tick.
	due := make([]int, 0, len(p.cfg.Reads))
	for i := range p.cfg.Reads {
		if !now.Before(p.nextExec[i]) {
			due = append(due, i)
		}
	}

	if len(due) == 0 {
		// No reads are scheduled for this tick.
		p.recordSuccess()
		return res
	}

	// Lazy connect: only attempt when there is work to do.
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

	for _, idx := range due {
		rb := p.cfg.Reads[idx]
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

	// All due reads succeeded: advance their next-execution times.
	for _, idx := range due {
		p.nextExec[idx] = now.Add(p.cfg.Reads[idx].Interval)
	}

	res.Blocks = blocks
	p.recordSuccess()
	return res
}

// minInterval returns the smallest interval across all read blocks.
// Used by Run to set the ticker resolution so no read block is late by more
// than one tick.  Precondition: cfg.Reads is non-empty (guaranteed by NewPoller).
func (p *Poller) minInterval() time.Duration {
	if len(p.cfg.Reads) == 0 {
		return time.Second // should never happen after successful NewPoller
	}
	min := p.cfg.Reads[0].Interval
	for _, r := range p.cfg.Reads[1:] {
		if r.Interval < min {
			min = r.Interval
		}
	}
	return min
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
