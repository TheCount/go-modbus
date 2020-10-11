package modbus

// Listener describes a Modbus listener.
type Listener interface {
	// Close closes the listener, stopping it from accepting new requests
	// and, for connection-based protocols, closing existing connections as well.
	Close() error
}
