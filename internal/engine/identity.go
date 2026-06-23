package engine

// Identity holds the resolved @me token. Preflight (in main) resolves the
// authenticated GitHub login once via the gh seam and stores it here with
// SetSelfLogin; the matcher (Slice 4) reads it with SelfLogin to expand the
// @me author token. Both are locked, consistent with the engine's mutex
// discipline, since the matcher runs on the cycle goroutine while main sets it
// at startup.

// SetSelfLogin stores the resolved @me login (locked write).
func (e *Engine) SetSelfLogin(login string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.selfLogin = login
}

// SelfLogin returns the resolved @me login (locked read).
func (e *Engine) SelfLogin() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.selfLogin
}
