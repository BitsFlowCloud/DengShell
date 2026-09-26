package app

// Unavailable means no usable probe could be sent (unsupported API, permissions,
// missing executable or cancellation). It must never be counted as packet loss.
type ICMPProbeResult struct {
	Address      string  `json:"address"`
	Milliseconds float64 `json:"milliseconds"`
	Status       string  `json:"status"`
	Error        string  `json:"error,omitempty"`
	TTLExpired   bool    `json:"ttlExpired,omitempty"`
	Responded    bool    `json:"responded,omitempty"`
}
