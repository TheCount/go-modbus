package modbus

// Address describes a Modbus low-level address.
type Address interface {
	// Protocol returns the low-level protocol associated with the address, e. g.,
	// "mbap", "mbaps", "rtu", or "ascii".
	Protocol() string

	// String returns a string representation of the address.
	String() string
}
