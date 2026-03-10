// internal/puller/engine_test.go
package puller

import (
"errors"
"testing"
"time"
)

// fakeClient is a minimal Client implementation for testing.
type fakeClient struct {
failFC uint8 // non-zero means reads for that FC return an error
}

func (f *fakeClient) ReadCoils(addr, qty uint16) ([]bool, error) {
if f.failFC == 1 {
return nil, errors.New("fc1 fail")
}
return make([]bool, qty), nil
}

func (f *fakeClient) ReadDiscreteInputs(addr, qty uint16) ([]bool, error) {
if f.failFC == 2 {
return nil, errors.New("fc2 fail")
}
return make([]bool, qty), nil
}

func (f *fakeClient) ReadHoldingRegisters(addr, qty uint16) ([]uint16, error) {
if f.failFC == 3 {
return nil, errors.New("fc3 fail")
}
return make([]uint16, qty), nil
}

func (f *fakeClient) ReadInputRegisters(addr, qty uint16) ([]uint16, error) {
if f.failFC == 4 {
return nil, errors.New("fc4 fail")
}
return make([]uint16, qty), nil
}

func newTestPoller(t *testing.T, reads []ReadBlock) *Poller {
t.Helper()
p, err := NewPoller(
PollerConfig{UnitID: "u1", Reads: reads},
&fakeClient{},
nil,
)
if err != nil {
t.Fatalf("NewPoller: %v", err)
}
return p
}

func TestPollAtEmptyTickDoesNotUpdateCounters(t *testing.T) {
p := newTestPoller(t, []ReadBlock{
{FC: 3, Address: 0, Quantity: 1, Interval: 10 * time.Second},
})

future := time.Now().Add(5 * time.Second)
p.nextExec[0] = future

p.counters.ConsecutiveFailCurr = 5
p.counters.RequestsTotal = 10
p.counters.ResponsesValidTotal = 8

now := time.Now()
res := p.pollAt(now)

if res.Err != nil {
t.Errorf("empty tick: expected no error, got %v", res.Err)
}
if len(res.Blocks) != 0 {
t.Errorf("empty tick: expected no blocks, got %d", len(res.Blocks))
}
if len(res.BlockUpdates) != 0 {
t.Errorf("empty tick: expected no block updates, got %d", len(res.BlockUpdates))
}

if p.counters.RequestsTotal != 10 {
t.Errorf("RequestsTotal: want 10 (unchanged), got %d", p.counters.RequestsTotal)
}
if p.counters.ResponsesValidTotal != 8 {
t.Errorf("ResponsesValidTotal: want 8 (unchanged), got %d", p.counters.ResponsesValidTotal)
}
if p.counters.ConsecutiveFailCurr != 5 {
t.Errorf("ConsecutiveFailCurr: want 5 (unchanged), got %d", p.counters.ConsecutiveFailCurr)
}
}

func TestPollAtSuccessUpdatesCounters(t *testing.T) {
p := newTestPoller(t, []ReadBlock{
{FC: 3, Address: 0, Quantity: 1, Interval: 10 * time.Second},
})

p.counters.ConsecutiveFailCurr = 3
p.counters.RequestsTotal = 7
p.counters.ResponsesValidTotal = 5

res := p.pollAt(time.Now())

if res.Err != nil {
t.Fatalf("expected success, got error: %v", res.Err)
}
if len(res.Blocks) != 1 {
t.Errorf("expected 1 block, got %d", len(res.Blocks))
}
if p.counters.RequestsTotal != 8 {
t.Errorf("RequestsTotal: want 8, got %d", p.counters.RequestsTotal)
}
if p.counters.ResponsesValidTotal != 6 {
t.Errorf("ResponsesValidTotal: want 6, got %d", p.counters.ResponsesValidTotal)
}
if p.counters.ConsecutiveFailCurr != 0 {
t.Errorf("ConsecutiveFailCurr: want 0, got %d", p.counters.ConsecutiveFailCurr)
}
}

func TestPollAtFailureUpdatesCounters(t *testing.T) {
reads := []ReadBlock{
{FC: 3, Address: 0, Quantity: 1, Interval: 10 * time.Second},
}
p, err := NewPoller(
PollerConfig{UnitID: "u1", Reads: reads},
&fakeClient{failFC: 3},
nil,
)
if err != nil {
t.Fatalf("NewPoller: %v", err)
}

p.counters.ConsecutiveFailCurr = 2
p.counters.RequestsTotal = 7
p.counters.ResponsesValidTotal = 5
p.counters.ConsecutiveFailMax = 2

res := p.pollAt(time.Now())

if res.Err == nil {
t.Fatal("expected failure, got success")
}
if p.counters.RequestsTotal != 8 {
t.Errorf("RequestsTotal: want 8, got %d", p.counters.RequestsTotal)
}
if p.counters.ResponsesValidTotal != 5 {
t.Errorf("ResponsesValidTotal: want 5 (unchanged), got %d", p.counters.ResponsesValidTotal)
}
if p.counters.ConsecutiveFailCurr != 3 {
t.Errorf("ConsecutiveFailCurr: want 3, got %d", p.counters.ConsecutiveFailCurr)
}
if p.counters.ConsecutiveFailMax != 3 {
t.Errorf("ConsecutiveFailMax: want 3, got %d", p.counters.ConsecutiveFailMax)
}
}

func TestPollAtMixedDueNotDue(t *testing.T) {
reads := []ReadBlock{
{FC: 3, Address: 0, Quantity: 1, Interval: 10 * time.Second},
{FC: 3, Address: 1, Quantity: 1, Interval: 10 * time.Second},
}
p, err := NewPoller(PollerConfig{UnitID: "u1", Reads: reads}, &fakeClient{}, nil)
if err != nil {
t.Fatalf("NewPoller: %v", err)
}

now := time.Now()
p.nextExec[1] = now.Add(8 * time.Second)

res := p.pollAt(now)

if res.Err != nil {
t.Fatalf("expected success, got error: %v", res.Err)
}

if len(res.Blocks) != 1 {
t.Errorf("Blocks: want 1, got %d", len(res.Blocks))
}
if len(res.BlockUpdates) != 1 {
t.Errorf("BlockUpdates: want 1, got %d", len(res.BlockUpdates))
}
if res.BlockUpdates[0].BlockIdx != 0 {
t.Errorf("BlockUpdates[0].BlockIdx: want 0 (block 0), got %d", res.BlockUpdates[0].BlockIdx)
}

if p.counters.RequestsTotal != 1 {
t.Errorf("RequestsTotal: want 1, got %d", p.counters.RequestsTotal)
}
if p.counters.ResponsesValidTotal != 1 {
t.Errorf("ResponsesValidTotal: want 1, got %d", p.counters.ResponsesValidTotal)
}

if p.nextExec[0].IsZero() {
t.Error("nextExec[0] must be advanced after execution")
}
if p.nextExec[1] != now.Add(8*time.Second) {
t.Errorf("nextExec[1] must not change: want %v, got %v",
now.Add(8*time.Second), p.nextExec[1])
}
}

func TestPollAtMultiBlockAllDue(t *testing.T) {
reads := []ReadBlock{
{FC: 1, Address: 0, Quantity: 2, Interval: time.Second},
{FC: 3, Address: 10, Quantity: 4, Interval: time.Second},
{FC: 4, Address: 20, Quantity: 3, Interval: time.Second},
}
p, err := NewPoller(PollerConfig{UnitID: "u1", Reads: reads}, &fakeClient{}, nil)
if err != nil {
t.Fatalf("NewPoller: %v", err)
}

res := p.pollAt(time.Now())

if res.Err != nil {
t.Fatalf("expected success, got error: %v", res.Err)
}

if len(res.Blocks) != 3 {
t.Errorf("Blocks: want 3, got %d", len(res.Blocks))
}
if len(res.BlockUpdates) != 3 {
t.Errorf("BlockUpdates: want 3, got %d", len(res.BlockUpdates))
}
for i, upd := range res.BlockUpdates {
if !upd.Success {
t.Errorf("BlockUpdates[%d].Success: want true, got false", i)
}
}

if p.counters.RequestsTotal != 1 {
t.Errorf("RequestsTotal: want 1 (one tick, not one per block), got %d",
p.counters.RequestsTotal)
}
if p.counters.ResponsesValidTotal != 1 {
t.Errorf("ResponsesValidTotal: want 1, got %d", p.counters.ResponsesValidTotal)
}
if p.counters.ConsecutiveFailCurr != 0 {
t.Errorf("ConsecutiveFailCurr: want 0, got %d", p.counters.ConsecutiveFailCurr)
}
}

func TestPollAtFailureAdvancesNextExec(t *testing.T) {
reads := []ReadBlock{
{FC: 3, Address: 0, Quantity: 1, Interval: 100 * time.Millisecond},
{FC: 3, Address: 1, Quantity: 1, Interval: 60 * time.Second},
}
p, err := NewPoller(
PollerConfig{UnitID: "u1", Reads: reads},
&fakeClient{failFC: 3},
nil,
)
if err != nil {
t.Fatalf("NewPoller: %v", err)
}

now := time.Now()
res := p.pollAt(now)

if res.Err == nil {
t.Fatal("expected failure, got success")
}

wantFast := now.Add(100 * time.Millisecond)
if p.nextExec[0].Before(wantFast) || p.nextExec[0].After(wantFast) {
t.Errorf("nextExec[0]: want %v, got %v", wantFast, p.nextExec[0])
}

wantSlow := now.Add(60 * time.Second)
if p.nextExec[1].Before(wantSlow) || p.nextExec[1].After(wantSlow) {
t.Errorf("nextExec[1]: want %v, got %v", wantSlow, p.nextExec[1])
}
}

func TestPollAtFactoryFailureAdvancesNextExec(t *testing.T) {
reads := []ReadBlock{
{FC: 3, Address: 0, Quantity: 1, Interval: 5 * time.Second},
}
p, err := NewPoller(
PollerConfig{UnitID: "u1", Reads: reads},
nil,
func() (Client, error) { return nil, errors.New("connection refused") },
)
if err != nil {
t.Fatalf("NewPoller: %v", err)
}

now := time.Now()
res := p.pollAt(now)

if res.Err == nil {
t.Fatal("expected factory failure, got success")
}

want := now.Add(5 * time.Second)
if p.nextExec[0].Before(want) || p.nextExec[0].After(want) {
t.Errorf("nextExec[0] after factory failure: want %v, got %v", want, p.nextExec[0])
}
}

func TestPollAtEmptyTickPreservesErrorState(t *testing.T) {
p := newTestPoller(t, []ReadBlock{
{FC: 3, Address: 0, Quantity: 1, Interval: 10 * time.Second},
})

p.counters.ConsecutiveFailCurr = 7
p.counters.ConsecutiveFailMax = 7
p.counters.RequestsTotal = 20
p.counters.ResponsesValidTotal = 13

p.nextExec[0] = time.Now().Add(8 * time.Second)

res := p.pollAt(time.Now())

if res.Err != nil || len(res.BlockUpdates) != 0 {
t.Skip("not an empty tick — test setup invalid")
}

if p.counters.ConsecutiveFailCurr != 7 {
t.Errorf("ConsecutiveFailCurr must not be reset on empty tick: want 7, got %d",
p.counters.ConsecutiveFailCurr)
}
if p.counters.RequestsTotal != 20 {
t.Errorf("RequestsTotal must not increment on empty tick: want 20, got %d",
p.counters.RequestsTotal)
}
if p.counters.ResponsesValidTotal != 13 {
t.Errorf("ResponsesValidTotal must not increment on empty tick: want 13, got %d",
p.counters.ResponsesValidTotal)
}
}
