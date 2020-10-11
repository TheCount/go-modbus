package modbus

const (
	// minPDULen is the minimum PDU length, in bytes.
	minPDULen = 1

	// maxPDULen is the maximum PDU length, in bytes.
	maxPDULen = 253
)

// ADU describes a Modbus application data unit.
type ADU interface {
	// UnitID returns the unit identifier.
	UnitID() UnitID

	// Function returns the function code of the called function.
	Function() FunctionCode

	// Data returns the request data (protocol data unit without function code).
	Data() []byte
}

// Message describes a Modbus message (i. e., ADU and its provenance).
type Message interface {
	// From returns the low level address of the sender of this message.
	From() Address

	// To returns the low level address of the receiver of this message.
	To() Address

	// ADU returns the application data unit.
	ADU() ADU
}
