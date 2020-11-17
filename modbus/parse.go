package modbus

import (
	"encoding/binary"
)

const (
	// maxReadBits is the maximum number of bits which can be read in a single
	// ReadCoils or ReadDiscreteInputs request.
	maxReadBits = 2000

	// maxWriteBits is the maximum number of bits which can be written in a
	// single WriteMultipleCoils request.
	maxWriteBits = 1968

	// maxReadWords is the maximum number of words which can be read in a single
	// ReadHoldingRegisters, ReadInputRegisters, or ReadWriteMultipleRegisters
	// request.
	maxReadWords = 125

	// maxWriteWords is the maximum number of words which can be written in a
	// single WriteMultipleRegisters request.
	maxWriteWords = 123

	// maxReadWriteWords is the maximum number of words which can be written in a
	// single ReadWriteMultipleRegisters request.
	maxReadWriteWords = 121
)

// Parser describes a parser of modbus request data.
type Parser struct {
}

// parseReadRequest parses a Modbus read request with the common 4-byte
// structure (2 bytes start address, 2 bytes number of values to read).
func (p *Parser) parseReadRequest(data []byte, maxNumValues int) (
	start uint16, n int, err error,
) {
	if len(data) != 4 {
		return 0, 0, ExceptionIllegalDataValue
	}
	start = binary.BigEndian.Uint16(data[0:2])
	n = int(binary.BigEndian.Uint16(data[2:4]))
	if n <= 0 || n > maxNumValues {
		return 0, 0, ExceptionIllegalDataValue
	}
	if int(start)+n > 1<<16 {
		return 0, 0, ExceptionIllegalDataAddress
	}
	return
}

// parseReadBits parses a Modbus ReadCoils or ReadDiscreteInputs request.
func (p *Parser) parseReadBits(data []byte) (start uint16, n int, err error) {
	return p.parseReadRequest(data, maxReadBits)
}

// ParseReadCoils parses a Modbus ReadCoils request. The given data should be
// the request data without the function code. It returns the modbus start
// address and the number of coils to read.
func (p *Parser) ParseReadCoils(data []byte) (start uint16, n int, err error) {
	return p.parseReadBits(data)
}

// ParseReadDiscreteInputs parses a Modbus ReadDiscreteInputs request.
// The given data should be the request data without the function code.
// It returns the modbus start address and the number of discrete inputs to
// read.
func (p *Parser) ParseReadDiscreteInputs(data []byte) (
	start uint16, n int, err error,
) {
	return p.parseReadBits(data)
}

// parseReadWords parses a Modbus ReadHoldingRegisters or
// ParseReadInputRegisters request.
func (p *Parser) parseReadWords(data []byte) (start uint16, n int, err error) {
	return p.parseReadRequest(data, maxReadWords)
}

// ParseReadHoldingRegisters parses a Modbus ReadHoldingRegisters request.
// The given data should be the request data without the function code.
// It returns the modbus start address and the number of holding registers to
// read.
func (p *Parser) ParseReadHoldingRegisters(data []byte) (
	start uint16, n int, err error,
) {
	return p.parseReadWords(data)
}

// ParseReadInputRegisters parses a Modbus ReadInputRegisters request.
// The given data should be the request data without the function code.
// It returns the modbus start address and the number of input registers to
// read.
func (p *Parser) ParseReadInputRegisters(data []byte) (
	start uint16, n int, err error,
) {
	return p.parseReadWords(data)
}

// ParseWriteSingleCoil parses a Modbus WriteSingleCoil request.
// The given data should be the request data without the function code.
// It returns the coil address and the value which should be written.
func (p *Parser) ParseWriteSingleCoil(data []byte) (
	addr uint16, value bool, err error,
) {
	if len(data) != 4 {
		return 0, false, ExceptionIllegalDataValue
	}
	addr = binary.BigEndian.Uint16(data[0:2])
	switch binary.BigEndian.Uint16(data[2:4]) {
	case 0x0000:
	case 0xFF00:
		value = true
	default:
		return 0, false, ExceptionIllegalDataValue
	}
	return
}

// ParseWriteSingleRegister parses a Modbus WriteSingleRegister request.
// The given data should be the request data without the function code.
// It returns the holding register address and the value which should be
// written.
func (p *Parser) ParseWriteSingleRegister(data []byte) (
	addr uint16, value uint16, err error,
) {
	if len(data) != 4 {
		return 0, 0, ExceptionIllegalDataValue
	}
	addr = binary.BigEndian.Uint16(data[0:2])
	value = binary.BigEndian.Uint16(data[2:4])
	return
}

// ParseWriteMultipleCoils parses a Modbus WriteMultipleCoils request.
// The given data should be the request data without the function code.
// It returns the start address, the number of bits to write, and the bit
// values which should be written in a slice of bytes. Each byte holds 8 bits,
// with lower addresses corresponding to less significant bits. If n is not
// divisible by 8, the unused bits of the last byte of values are zero.
func (p *Parser) ParseWriteMultipleCoils(data []byte) (
	start uint16, n int, values []byte, err error,
) {
	if len(data) < 5 {
		return 0, 0, nil, ExceptionIllegalDataValue
	}
	start = binary.BigEndian.Uint16(data[0:2])
	n = int(binary.BigEndian.Uint16(data[2:4]))
	numBytes := (n + 7) / 8
	if n <= 0 || n > maxWriteBits ||
		numBytes != int(data[4]) || len(data)-5 != numBytes {
		return 0, 0, nil, ExceptionIllegalDataValue
	}
	values = data[5:]
	shift := 8 - (n % 8)
	if data[len(data)-1]>>shift != 0 {
		return 0, 0, nil, ExceptionIllegalDataValue
	}
	if int(start)+n > 1<<16 {
		return 0, 0, nil, ExceptionIllegalDataAddress
	}
	return
}

// ParseWriteMultipleRegisters parses a Modbus WriteMultipleRegisters request.
// The given data should be the request data without the function code.
// It returns the start address and the data to write in big endian.
// The number of registers to write is half the length of the returned values.
func (p *Parser) ParseWriteMultipleRegisters(data []byte) (
	start uint16, values []byte, err error,
) {
	if len(data) < 5 {
		return 0, nil, ExceptionIllegalDataValue
	}
	start = binary.BigEndian.Uint16(data[0:2])
	n := int(binary.BigEndian.Uint16(data[2:4]))
	numBytes := int(data[4])
	if n <= 0 || n > maxWriteWords ||
		2*n != numBytes || len(data)-5 != numBytes {
		return 0, nil, ExceptionIllegalDataValue
	}
	if int(start)+n > 1<<16 {
		return 0, nil, ExceptionIllegalDataAddress
	}
	values = data[5:]
	return
}

// ParseMaskWriteRegister parses a Modbus MaskWriteRegister request.
// The given data should be the request data without the function code.
// It returns the register address, and mask, and or mask.
func (p *Parser) ParseMaskWriteRegister(data []byte) (
	addr, and, or uint16, err error,
) {
	if len(data) != 6 {
		return 0, 0, 0, ExceptionIllegalDataValue
	}
	addr = binary.BigEndian.Uint16(data[0:2])
	and = binary.BigEndian.Uint16(data[2:4])
	or = binary.BigEndian.Uint16(data[4:6])
	return
}

// ParseReadWriteMultipleRegisters parses a Modbus ReadWriteMultipleRegisters
// request. The given data should be the request data without the function code.
// It returns the read and write start addresses, the number of registers to
// read, and the data to write in big endian. The number of registers to write
// is half the length of the returned values.
func (p *Parser) ParseReadWriteMultipleRegisters(data []byte) (
	readStart uint16, n int, writeStart uint16, values []byte, err error,
) {
	if len(data) < 9 {
		return 0, 0, 0, nil, ExceptionIllegalDataValue
	}
	readStart = binary.BigEndian.Uint16(data[0:2])
	n = int(binary.BigEndian.Uint16(data[2:4]))
	writeStart = binary.BigEndian.Uint16(data[4:6])
	nWrite := int(binary.BigEndian.Uint16(data[6:8]))
	numBytes := int(data[8])
	if n <= 0 || n > maxReadWords || nWrite <= 0 || nWrite > maxReadWriteWords ||
		2*nWrite != numBytes || len(data)-9 != numBytes {
		return 0, 0, 0, nil, ExceptionIllegalDataValue
	}
	if int(readStart)+n > 1<<16 || int(writeStart)+nWrite > 1<<16 {
		return 0, 0, 0, nil, ExceptionIllegalDataAddress
	}
	values = data[9:]
	return
}
