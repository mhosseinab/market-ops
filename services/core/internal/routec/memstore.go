package routec

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// MemKillSwitchStore is an in-memory KillSwitchStore for offline tests and the
// measurement harness. It mirrors the durable store's layered semantics without a
// database so unit tests run without DATABASE_URL. It is NOT used by the running
// binary — that path uses the durable DB store so a stop survives restart.
type MemKillSwitchStore struct {
	mu       sync.Mutex
	global   bool
	accounts map[uuid.UUID]string
	targets  map[uuid.UUID]string
}

// NewMemKillSwitchStore builds an empty in-memory store.
func NewMemKillSwitchStore() *MemKillSwitchStore {
	return &MemKillSwitchStore{
		accounts: make(map[uuid.UUID]string),
		targets:  make(map[uuid.UUID]string),
	}
}

func (m *MemKillSwitchStore) Engaged(context.Context) ([]Switch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Switch
	if m.global {
		out = append(out, Switch{Scope: KillGlobal})
	}
	for a, r := range m.accounts {
		out = append(out, Switch{Scope: KillAccount, Account: a, Reason: r})
	}
	for t, r := range m.targets {
		out = append(out, Switch{Scope: KillTarget, TargetID: t, Reason: r})
	}
	return out, nil
}

func (m *MemKillSwitchStore) EngageGlobal(_ context.Context, _ string, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.global = true
	return nil
}

func (m *MemKillSwitchStore) EngageAccount(_ context.Context, account uuid.UUID, reason string, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts[account] = reason
	return nil
}

func (m *MemKillSwitchStore) EngageTarget(_ context.Context, _, target uuid.UUID, reason string, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets[target] = reason
	return nil
}

func (m *MemKillSwitchStore) DisengageGlobal(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.global = false
	return nil
}

func (m *MemKillSwitchStore) DisengageAccount(_ context.Context, account uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accounts, account)
	return nil
}

func (m *MemKillSwitchStore) DisengageTarget(_ context.Context, target uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.targets, target)
	return nil
}
