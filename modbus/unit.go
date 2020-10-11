package modbus

// UnitID describes a Modbus unit identifier. The UnitID identifies a Modbus
// server device.
type UnitID uint8

// Unit identifier constants.
const (
	// UnitBroadcast is the unit identifier used for broadcasts over a serial
	// line.
	UnitBroadcast UnitID = 0

	// UnitIndividualMin is the minimum valid unit ID for an individual serial
	// Modbus device.
	UnitIndividualMin UnitID = 1

	// UnitIndividualMax is the maximum valid unit ID for an individual serial
	// Modbus device.
	UnitIndividualMax UnitID = 247

	// UnitTCP is the unit identifier for a Modbus/TCP device.
	UnitTCP UnitID = 255
)

// IsValidSerial checks whether this unit identifier is valid for
// an individual serial Modbus device.
func (uid UnitID) IsValidSerial() bool {
	return uid >= UnitIndividualMin && uid <= UnitIndividualMax
}

// IsValid checks whether this unit identifier is valid, either for broadcasts
// over a serial line, for an individual serial Modbus device, or for a
// Modbus/TCP server.
func (uid UnitID) IsValid() bool {
	return uid == UnitTCP || uid <= UnitIndividualMax
}
