package vars

import (
	"fmt"
	"strings"
	"sync"
)

// CredVarsTracker implements the interface Variables. It wraps a secret manager and
// tracks key-values fetched from the secret managers. It also provides a method to
// thread-safely iterate interpolated key-values.

type CredVarsTrackerIterator interface {
	YieldCred(string, string)
}

//go:generate counterfeiter . CredVarsTracker

type CredVarsTracker interface {
	Variables
	IterateInterpolatedCreds(iter CredVarsTrackerIterator)
	Enabled() bool

	NewLocalScope() CredVarsTracker
	AddLocalVar(string, interface{}, bool)
}

func NewCredVarsTracker(credVars Variables, on bool) CredVarsTracker {
	return &credVarsTracker{
		localVars:         StaticVariables{},
		credVars:          credVars,
		enabled:           on,
		interpolatedCreds: map[string]string{},
		noRedactVarNames:  map[string]bool{},
	}
}

type credVarsTracker struct {
	credVars  Variables
	localVars StaticVariables

	enabled bool

	interpolatedCreds map[string]string

	noRedactVarNames map[string]bool

	// Considering in-parallel steps, a lock is need.
	lock sync.RWMutex
}

func (t *credVarsTracker) Get(varDef VariableDefinition) (interface{}, bool, error) {
	var (
		val   interface{}
		found bool
		err   error
	)

	redact := true
	parts := strings.Split(varDef.Name, ":")
	if len(parts) == 2 && parts[0] == "." {
		varDef.Name = parts[1]
		val, found, err = t.localVars.Get(varDef)
		if found {
			parts = strings.Split(varDef.Name, ".")
			if _, ok := t.noRedactVarNames[parts[0]]; ok {
				redact = false
			}
		}
	} else {
		val, found, err = t.credVars.Get(varDef)
	}

	if t.enabled && found && redact {
		t.lock.Lock()
		t.track(varDef.Name, val)
		t.lock.Unlock()
	}

	return val, found, err
}

func (t *credVarsTracker) track(name string, val interface{}) {
	switch v := val.(type) {
	case map[interface{}]interface{}:
		for kk, vv := range v {
			nn := fmt.Sprintf("%s.%s", name, kk.(string))
			t.track(nn, vv)
		}
	case map[string]interface{}:
		for kk, vv := range v {
			nn := fmt.Sprintf("%s.%s", name, kk)
			t.track(nn, vv)
		}
	case string:
		t.interpolatedCreds[name] = v
	default:
		// Do nothing
	}
}

func (t *credVarsTracker) List() ([]VariableDefinition, error) {
	return t.credVars.List()
}

func (t *credVarsTracker) IterateInterpolatedCreds(iter CredVarsTrackerIterator) {
	t.lock.RLock()
	for k, v := range t.interpolatedCreds {
		iter.YieldCred(k, v)
	}
	t.lock.RUnlock()
}

func (t *credVarsTracker) Enabled() bool {
	return t.enabled
}

func (t *credVarsTracker) NewLocalScope() CredVarsTracker {
	localVarsClone := make(StaticVariables, len(t.localVars))
	for k, v := range t.localVars {
		localVarsClone[k] = v
	}
	noRedactVarNamesClone := make(map[string]bool, len(t.noRedactVarNames))
	for k, v := range t.noRedactVarNames {
		noRedactVarNamesClone[k] = v
	}
	interpolatedCredsClone := MapCredVarsTrackerIterator{}
	t.IterateInterpolatedCreds(interpolatedCredsClone)
	return &credVarsTracker{
		credVars:          t.credVars,
		enabled:           t.enabled,
		localVars:         localVarsClone,
		noRedactVarNames:  noRedactVarNamesClone,
		interpolatedCreds: interpolatedCredsClone,
	}
}

func (t *credVarsTracker) AddLocalVar(name string, value interface{}, redact bool) {
	t.localVars[name] = value
	if !redact {
		t.noRedactVarNames[name] = true
	}
}

// MapCredVarsTrackerIterator implements a simple CredVarsTrackerIterator which just
// populate interpolated secrets into a map.
type MapCredVarsTrackerIterator map[string]string

func (it MapCredVarsTrackerIterator) YieldCred(k, v string) {
	it[k] = v
}
