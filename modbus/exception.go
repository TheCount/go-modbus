package modbus

import (
	"fmt"
)

// ExceptionCode describes a Modbus exception response code.
type ExceptionCode uint8

// Exception code constants.
const (
	ExceptionIllegalFunction                    ExceptionCode = 0x01
	ExceptionIllegalDataAddress                 ExceptionCode = 0x02
	ExceptionIllegalDataValue                   ExceptionCode = 0x03
	ExceptionServerDeviceFailure                ExceptionCode = 0x04
	ExceptionAcknowledge                        ExceptionCode = 0x05
	ExceptionServerDeviceBusy                   ExceptionCode = 0x06
	ExceptionMemoryParityError                  ExceptionCode = 0x08
	ExceptionGatewayPathUnavailable             ExceptionCode = 0x0A
	ExceptionGatewayTargetDeviceFailedToRespond ExceptionCode = 0x0B
)

// exceptionStrings maps known exceptions to a textual representation.
var exceptionStrings = map[ExceptionCode]string{
	ExceptionIllegalFunction:                    "illegal function",
	ExceptionIllegalDataAddress:                 "illegal data address",
	ExceptionIllegalDataValue:                   "illegal data value",
	ExceptionServerDeviceFailure:                "server device failure",
	ExceptionAcknowledge:                        "acknowledge",
	ExceptionServerDeviceBusy:                   "server device busy",
	ExceptionMemoryParityError:                  "memory parity error",
	ExceptionGatewayPathUnavailable:             "gateway path unavailable",
	ExceptionGatewayTargetDeviceFailedToRespond: "gateway target failed to respond",
}

// Error returns a textual representation of the exception represented by
// this exception code.
func (ec ExceptionCode) Error() string {
	s, ok := exceptionStrings[ec]
	if !ok {
		s = fmt.Sprintf("unknown exception %02X", ec)
	}
	return "Modbus exception: " + s
}
