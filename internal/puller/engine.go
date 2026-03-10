// internal/puller/engine.go
// Poller: per-unit Modbus read scheduler with independent per-block cadences.
package puller

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/tamzrod/Aegis/internal/memory"
)

// Client abstracts the Modbus read operations needed by the poller.
type Client interface {
	ReadCoils(addr, qty uint16) ([]bool, error)
	ReadDiscreteInputs(addr, qty uint16) ([]bool, error)
	ReadHoldingRegisters(addr, qty uint16) ([]uint16, error)
	ReadInputRegisters(addr, qty uint16) ([]uint16, error)
}

// modbusExceptionErr is satisfied by errors that carry a Modbus exception code.
type modbusExceptionErr interface {
	Code() uint16
}

// PollerConfig is the minimal runtime config the poller needs.
type PollerConfig struct {
	UnitID string
	Reads  []ReadBlock
}

// Poller reads from a field device via a Client.
// It maintains an independent schedule per read block so that fast-moving
// and slow-moving data can be read at different cadences without spawning
// a goroutine per block.
type Poller struct {
	cfg PollerConfig

	client  Client
	factory func() (Client, error)

	nextExec []time.Time

	counters TransportCounters
}

// NewPoller creates a Poller with immutable config.
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
		nextExec: make([]time.Time, len(cfg.Reads)),
	}, nil
}

// PollOnce performs one scheduling tick at the current wall time.
func (p *Poller) PollOnce() memory.PollResult {
	return p.pollAt(time.Now())
}

// pollAt is the internal implementation of PollOnce with an explicit clock value.
func (p *Poller) pollAt(now time.Time) memory.PollResult {
	res := memory.PollResult{
		UnitID: p.cfg.UnitID,
		At:     now,
	}

	due := make([]int, 0, len(p.cfg.Reads))
	for i := range p.cfg.Reads {
		if !now.Before(p.nextExec[i]) {
			due = append(due, i)
		}
	}

	if len(due) == 0 {
		return res
	}

	for _, idx := range due {
		p.nextExec[idx] = now.Add(p.cfg.Reads[idx].Interval)
	}

	p.counters.RequestsTotal++

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

	var blocks []memory.BlockResult
	var updates []memory.BlockUpdate

	for _, idx := range due {
		rb := p.cfg.Reads[idx]
		switch rb.FC {
		case 1:
			bits, err := p.client.ReadCoils(rb.Address, rb.Quantity)
			if err != nil {
				p.maybeInvalidateClient(err)
				res.Err = err
				updates = append(updates, blockUpdateFromErr(idx, err))
				p.recordFailure(err)
				res.BlockUpdates = updates
				return res
			}
			blocks = append(blocks, memory.BlockResult{
				FC: rb.FC, Address: rb.Address, Quantity: rb.Quantity, Bits: bits,
			})
			updates = append(updates, memory.BlockUpdate{BlockIdx: idx, Success: true})

		case 2:
			bits, err := p.client.ReadDiscreteInputs(rb.Address, rb.Quantity)
			if err != nil {
				p.maybeInvalidateClient(err)
				res.Err = err
				updates = append(updates, blockUpdateFromErr(idx, err))
				p.recordFailure(err)
				res.BlockUpdates = updates
				return res
			}
			blocks = append(blocks, memory.BlockResult{
				FC: rb.FC, Address: rb.Address, Quantity: rb.Quantity, Bits: bits,
			})
			updates = append(updates, memory.BlockUpdate{BlockIdx: idx, Success: true})

		case 3:
			regs, err := p.client.ReadHoldingRegisters(rb.Address, rb.Quantity)
			if err != nil {
				p.maybeInvalidateClient(err)
				res.Err = err
				updates = append(updates, blockUpdateFromErr(idx, err))
				p.recordFailure(err)
				res.BlockUpdates = updates
				return res
			}
			blocks = append(blocks, memory.BlockResult{
				FC: rb.FC, Address: rb.Address, Quantity: rb.Quantity, Registers: regs,
			})
			updates = append(updates, memory.BlockUpdate{BlockIdx: idx, Success: true})

		case 4:
			regs, err := p.client.ReadInputRegisters(rb.Address, rb.Quantity)
			if err != nil {
				p.maybeInvalidateClient(err)
				res.Err = err
				updates = append(updates, blockUpdateFromErr(idx, err))
				p.recordFailure(err)
				res.BlockUpdates = updates
				return res
			}
			blocks = append(blocks, memory.BlockResult{
				FC: rb.FC, Address: rb.Address, Quantity: rb.Quantity, Registers: regs,
			})
			updates = append(updates, memory.BlockUpdate{BlockIdx: idx, Success: true})

		default:
			res.Err = errors.New("poller: unsupported function code")
			p.recordFailure(res.Err)
			res.BlockUpdates = updates
			return res
		}
	}

	res.Blocks = blocks
	res.BlockUpdates = updates
	p.recordSuccess()
	return res
}

// blockUpdateFromErr constructs a BlockUpdate for a failed read.
func blockUpdateFromErr(blockIdx int, err error) memory.BlockUpdate {
	u := memory.BlockUpdate{BlockIdx: blockIdx, Success: false}

	var excErr modbusExceptionErr
	if errors.As(err, &excErr) {
		u.ExceptionCode = byte(excErr.Code())
		return u
	}

	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		u.Timeout = true
		return u
	}

	u.Timeout = true
	return u
}

// minInterval returns the smallest interval across all read blocks.
func (p *Poller) minInterval() time.Duration {
	if len(p.cfg.Reads) == 0 {
		return time.Second
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

func (p *Poller) maybeInvalidateClient(err error) {
	if err == nil || !isDeadConnErr(err) {
		return
	}
	if c, ok := p.client.(interface{ Close() error }); ok {
		_ = c.Close()
	}
	p.client = nil
}

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
