package modbus

import (
	"sort"
)

// FunctionCode describes a Modbus function code.
type FunctionCode uint8

// Function code constants.
const (
	FunctionReadCoils                  FunctionCode = 1
	FunctionReadDiscreteInputs         FunctionCode = 2
	FunctionReadHoldingRegisters       FunctionCode = 3
	FunctionReadInputRegisters         FunctionCode = 4
	FunctionWriteSingleCoil            FunctionCode = 5
	FunctionWriteSingleRegister        FunctionCode = 6
	FunctionReadExceptionStatus        FunctionCode = 7
	FunctionDiagnostic                 FunctionCode = 8
	FunctionGetComEventCounter         FunctionCode = 11
	FunctionGetComEventLog             FunctionCode = 12
	FunctionWriteMultipleCoils         FunctionCode = 15
	FunctionWriteMultipleRegisters     FunctionCode = 16
	FunctionReportServerID             FunctionCode = 17
	FunctionReadFileRecord             FunctionCode = 20
	FunctionWriteFileRecord            FunctionCode = 21
	FunctionMaskWriteRegister          FunctionCode = 22
	FunctionReadWriteMultipleRegisters FunctionCode = 23
	FunctionReadFIFOQueue              FunctionCode = 24
	FunctionReadDeviceID               FunctionCode = 43
)

// Ranges for user defined functions.
const (
	FunctionUserDefined1Start FunctionCode = 65
	FunctionUserDefined1End   FunctionCode = 72
	FunctionUserDefined2Start FunctionCode = 100
	FunctionUserDefined2End   FunctionCode = 110
)

// FunctionError is the bit in the function code which determines
// whether the function was successful or not.
const FunctionError FunctionCode = 0x80

// reservedFunctionCodes is the list of reserved function codes.
// See Annex A of the Modbus Application Protocol specification.
// It must be sorted in increasing order.
var reservedFunctionCodes = [...]FunctionCode{
	9, 10, 13, 14, 41, 42, 90, 91, 125, 126, 127,
}

// IsReserved determines whether this is a reserved function code.
func (fc FunctionCode) IsReserved() bool {
	fc &^= FunctionError
	idx := sort.Search(len(reservedFunctionCodes), func(i int) bool {
		return fc <= reservedFunctionCodes[i]
	})
	return idx < len(reservedFunctionCodes) && fc == reservedFunctionCodes[idx]
}

// IsUserDefined determines whether the function with this function code is
// user defined.
func (fc FunctionCode) IsUserDefined() bool {
	fc &^= FunctionError
	return (fc >= FunctionUserDefined1Start && fc <= FunctionUserDefined1End) ||
		(fc >= FunctionUserDefined2Start && fc <= FunctionUserDefined2End)
}

// IsError determines whether this function code is from an error
// response.
func (fc FunctionCode) IsError() bool {
	return fc&FunctionError != 0
}

// AsError returns this function code with the error response bit set.
func (fc FunctionCode) AsError() FunctionCode {
	return fc | FunctionError
}
