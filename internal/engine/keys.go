package engine

import "time"

// The keys overlay (builtin keys.show): the engine decides when it is up and
// tells OnKeys; the daemon draws it.
//
// The gesture that showed it decides how long it stays:
//   - hold or down: until the key that showed it is released; other keys
//     can be pressed meanwhile and still act;
//   - press (or a dial turn or stick direction): KeysFor, or until the next
//     pad input other than the release of that same press.

// KeysFor is how long the keys overlay stays when a press showed it.
const KeysFor = 5 * time.Second

func (e *Engine) showKeys(name string) {
	held := e.gesture == "hold" || e.gesture == "down"
	e.keysShown, e.keysKey, e.keysHeld, e.keysUntil = true, name, held, e.now.Add(KeysFor)
	if e.OnKeys != nil {
		e.OnKeys(true)
	}
}

// keysInput hides the overlay when an input ends it; Input calls it with the
// logical control (MIC once both its switches are one key, before the
// input acts) and whether it is that key's release. The input still acts.
func (e *Engine) keysInput(name string, release bool) {
	if !e.keysShown {
		return
	}
	ownRelease := release && name == e.keysKey
	if e.keysHeld != ownRelease {
		// Held: only its own release ends it. Pressed: anything but the
		// release of that press does.
		return
	}
	e.hideKeys()
}

// keysTimeout hides an overlay a press showed once KeysFor has passed.
func (e *Engine) keysTimeout(now time.Time) {
	if e.keysShown && !e.keysHeld && !now.Before(e.keysUntil) {
		e.hideKeys()
	}
}

func (e *Engine) hideKeys() {
	if !e.keysShown {
		return
	}
	e.keysShown, e.keysKey, e.keysHeld = false, "", false
	if e.OnKeys != nil {
		e.OnKeys(false)
	}
}

// KeysShown reports whether the keys overlay is up.
func (e *Engine) KeysShown() bool { return e.keysShown }
