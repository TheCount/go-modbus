package modbus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// allDataFunctions is the list of all function codes which affect the
// Modbus data model. Must be sorted in ascending order.
var allDataFunctions = [...]FunctionCode{
	FunctionReadCoils,
	FunctionReadDiscreteInputs,
	FunctionReadHoldingRegisters,
	FunctionReadInputRegisters,
	FunctionWriteSingleCoil,
	FunctionWriteSingleRegister,
	FunctionWriteMultipleCoils,
	FunctionWriteMultipleRegisters,
	FunctionMaskWriteRegister,
	FunctionReadWriteMultipleRegisters,
}

// DataType enumerates data types for the Modbus data model.
type DataType uint8

// Data types
const (
	DataTypeDiscreteInputs DataType = iota
	DataTypeCoils
	DataTypeInputRegisters
	DataTypeHoldingRegisters
	numDataTypes = 4
)

// dataTypeNames are the names of the data types.
var dataTypeNames = [numDataTypes]string{
	"Discrete Inputs",
	"Coils",
	"Input Registers",
	"Holding Registers",
}

// numBits is the number of addressed bits per address for the data types.
var numBits = [numDataTypes]int{1, 1, 16, 16}

// String renders this data type as a string.
func (dt DataType) String() string {
	if int(dt) < len(dataTypeNames) {
		return dataTypeNames[dt]
	}
	return fmt.Sprintf("unknown data type %d", dt)
}

// NumBits returns the number of addressed bits per address for this data type.
// It panics if the data type is not known.
func (dt DataType) NumBits() int {
	return numBits[dt]
}

// DataModel describes a Modbus data model (see § 4.3 of the Modbus Application
// protocol specification).
type DataModel struct {
	// Ranges is the list of data ranges in the data model. Each range is
	// allocated its own block of memory which can be changed atomically.
	Ranges []DataRange

	// Aliases is a list of data aliases, which must alias memory defined in
	// Ranges.
	Aliases []DataAlias
}

// DataRange defines a continuous stretch of memory addresses in the Modbus
// data model.
type DataRange struct {
	// Type is the type of data for this range.
	Type DataType

	// StartAddress is the address of the first data element in the range
	// (indexed from zero).
	StartAddress uint16

	// Len is the length of the data range. Must be positive.
	Len uint16
}

// Validate checks whether this data range is valid.
func (dr DataRange) Validate() error {
	if dr.Type >= numDataTypes {
		return errors.New("unknown data type")
	}
	if dr.Len == 0 {
		return errors.New("zero length range")
	}
	end := dr.StartAddress + dr.Len
	if end > 0 && end < dr.StartAddress {
		return errors.New("length exceeds address space")
	}
	return nil
}

// DataAlias defines a continuous stretch of mirrored memory addresses in the
// Modbus data model.
type DataAlias struct {
	// Range is the data range defined by this alias.
	Range DataRange

	// Type is the data type of the original memory.
	Type DataType

	// StartAddress is the start address of the original memory.
	// StartAddress may point into the middle of an original data range, but
	// the length in Range must fit into the original range.
	StartAddress uint16

	// StartBit is the bit at StartAddress where to start the aliasing.
	// This field is used only if the defined range uses a bit type (discrete
	// inputs or coils) while the original memory uses a word type (input or
	// holding registers). Bit 0 is the least significant bit, Bit 15 the most
	// significant one.
	StartBit uint8
}

// Validate validates this data alias.
func (da *DataAlias) Validate() error {
	if err := da.Range.Validate(); err != nil {
		return fmt.Errorf("alias range: %w", err)
	}
	if da.Type >= numDataTypes {
		return fmt.Errorf("unknown original type %d", da.Type)
	}
	if da.Range.Type.NumBits()%8 != 0 && da.Type.NumBits()%8 == 0 {
		if da.StartBit >= 16 {
			return errors.New("start bit must be in [0,16)")
		}
	} else {
		if da.StartBit != 0 {
			return errors.New("cannot use StartBit in this context")
		}
	}
	return nil
}

// dataBlock describes a basic block of memory in the Modbus data model.
type dataBlock struct {
	// mx synchronises access to this data block.
	mx sync.RWMutex

	// data is the raw data of this data block.
	data []byte
}

// dataRef references data in a data block.
type dataRef struct {
	// block points to the referenced data block.
	block *dataBlock

	// blockOffset is the number of bits into block where startBit is located.
	blockOffset int

	// startBit is the start of the range of this reference.
	startBit int

	// numBits is the length of the referenced data in bits. Must fit in block,
	// i. e., blockOffset + numBits must be smaller than the length of
	// block in bits.
	numBits int
}

// Data represents Modbus data model data installed in a Modbus server.
type Data struct {
	// refs is the lists of data references according to the DataModel from
	// which this Data was created. There is one list for each data type.
	refs [numDataTypes][]dataRef
}

// NewData creates a new data backend specified by the given model.
func NewData(model DataModel) (*Data, error) {
	// Add all original data ranges with their own dedicated data block.
	result := &Data{}
	for i, dr := range model.Ranges {
		if err := dr.Validate(); err != nil {
			return nil, fmt.Errorf("data range %d invalid: %w", i, err)
		}
		startBit := dr.Type.NumBits() * int(dr.StartAddress)
		numBits := dr.Type.NumBits() * int(dr.Len)
		result.refs[dr.Type] = append(result.refs[dr.Type], dataRef{
			block: &dataBlock{
				data: make([]byte, (numBits+7)/8),
			},
			startBit: startBit,
			numBits:  numBits,
		})
	}
	lens := make([]int, numDataTypes)
	for i := 0; i != numDataTypes; i++ {
		refs := result.refs[i]
		sort.Slice(refs, func(j, k int) bool {
			return refs[j].startBit < refs[k].startBit
		})
		lens[i] = len(refs)
	}
	// Add alias ranges.
	for i, da := range model.Aliases {
		if err := da.Validate(); err != nil {
			return nil, fmt.Errorf("alias range %d invalid: %w", i, err)
		}
		alias := dataRef{
			startBit: da.Range.Type.NumBits() * int(da.Range.StartAddress),
			numBits:  da.Range.Type.NumBits() * int(da.Range.Len),
		}
		// Find matching block
		startBit := da.Type.NumBits() * int(da.StartAddress)
		refs := result.refs[da.Type][:lens[da.Type]]
		idx := sort.Search(lens[da.Type], func(i int) bool {
			return startBit < refs[i].startBit
		})
		if idx == 0 {
			return nil, fmt.Errorf("alias range %d points before first data range", i)
		}
		dr := &refs[idx-1]
		// Verify and add alias
		alias.block = dr.block
		alias.blockOffset = startBit - dr.startBit
		if da.Type.NumBits()%8 == 0 && da.Range.Type.NumBits()%8 != 0 {
			alias.blockOffset += int(da.StartBit)
		}
		if (alias.blockOffset+alias.numBits+7)/8 > len(alias.block.data) {
			return nil, fmt.Errorf("alias range %d overflows data range", i)
		}
		result.refs[da.Range.Type] = append(result.refs[da.Range.Type], alias)
	}
	// Check that there is no overlap
	for i := DataType(0); i != numDataTypes; i++ {
		refs := result.refs[i]
		sort.Slice(refs, func(j, k int) bool {
			return refs[j].startBit < refs[k].startBit
		})
		smallestLegalAddr := 0
		for _, ref := range refs {
			if ref.startBit < smallestLegalAddr {
				return nil, fmt.Errorf(
					"data range for %s starting at %d overlaps with previous range",
					i, ref.startBit/i.NumBits())
			}
			smallestLegalAddr = ref.startBit + ref.numBits
		}
	}
	return result, nil
}

// AddToServer adds this data backend to the specified server for the given
// unit. Normally, the data backend will be added for
// all function codes relevant to the Modbus data model. Optionally, if it is
// desired to add the data model only for a restricted set of function codes
// (e. g. because the server already uses other backends for some function
// codes), these can be given as arguments.
// It is permissible to add a single data model to multiple servers.
func (d *Data) AddToServer(
	srv *Server, unit UnitID, functions ...FunctionCode,
) error {
	// Check functions arg
	if len(functions) == 0 {
		functions = allDataFunctions[:]
	} else {
		sort.Slice(functions, func(i, j int) bool {
			return functions[i] < functions[j]
		})
		for i := 1; i < len(functions); i++ {
			if functions[i-1] == functions[i] {
				return fmt.Errorf("duplicate function code %d", functions[i])
			}
		}
		for _, f := range functions {
			idx := sort.Search(len(allDataFunctions), func(i int) bool {
				return f <= allDataFunctions[i]
			})
			if idx == len(allDataFunctions) || f != allDataFunctions[idx] {
				return fmt.Errorf("invalid data function code %d", f)
			}
		}
	}
	// Add to server
	if srv == nil {
		return errors.New("nil server")
	}
	return srv.SetFunctionHandler(d.FunctionHandler, unit, functions...)
}

// FunctionHandler is the Modbus function handler for the specified data.
func (d *Data) FunctionHandler(
	ctx context.Context, request Message, srv *Server,
) ([]byte, error) {
	adu := request.ADU()
	switch adu.Function() {
	case FunctionReadCoils:
		addr, n, err := ParseReadCoils(adu.Data())
		if err != nil {
			return nil, err
		}
		response := make([]byte, 1, n/8+2)
		response[0] = byte((n + 7) / 8)
		return d.readData(response, DataTypeCoils, addr, n)
	case FunctionReadDiscreteInputs:
		addr, n, err := ParseReadDiscreteInputs(adu.Data())
		if err != nil {
			return nil, err
		}
		response := make([]byte, 1, n/8+2)
		response[0] = byte((n + 7) / 8)
		if n%8 != 0 {
			response[0]++
		}
		return d.readData(response, DataTypeDiscreteInputs, addr, n)
	case FunctionReadHoldingRegisters:
		addr, n, err := ParseReadHoldingRegisters(adu.Data())
		if err != nil {
			return nil, err
		}
		response := make([]byte, 1, 2*n+1)
		response[0] = byte(2 * n)
		return d.readData(response, DataTypeHoldingRegisters, addr, n)
	case FunctionReadInputRegisters:
		addr, n, err := ParseReadInputRegisters(adu.Data())
		if err != nil {
			return nil, err
		}
		response := make([]byte, 1, 2*n+1)
		response[0] = byte(2 * n)
		return d.readData(response, DataTypeInputRegisters, addr, n)
	case FunctionWriteSingleCoil:
		addr, v, err := ParseWriteSingleCoil(adu.Data())
		if err != nil {
			return nil, err
		}
		wData := []byte{0}
		if v {
			wData[0] = 1
		}
		if err := d.writeData(DataTypeCoils, addr, wData); err != nil {
			return nil, err
		}
		return adu.Data(), nil
	case FunctionWriteSingleRegister:
		addr, v, err := ParseWriteSingleRegister(adu.Data())
		if err != nil {
			return nil, err
		}
		wData := make([]byte, 2)
		binary.BigEndian.PutUint16(wData, uint16(v))
		if err := d.writeData(
			DataTypeHoldingRegisters, addr, wData,
		); err != nil {
			return nil, err
		}
		return adu.Data(), nil
	case FunctionWriteMultipleCoils:
		addr, n, v, err := ParseWriteMultipleCoils(adu.Data())
		if err != nil {
			return nil, err
		}
		if err := d.writeData(DataTypeCoils, addr, v); err != nil {
			return nil, err
		}
		return adu.Data()[:4], nil
	case FunctionWriteMultipleRegisters:
		addr, n, v, err := ParseWriteMultipleRegisters(adu.Data())
		if err != nil {
			return nil, err
		}
		if err := d.writeData(DataTypeHoldingRegisters, addr, v); err != nil {
			return nil, err
		}
		return adu.Data()[:4], nil
	case FunctionMaskWriteRegister:
		addr, and, or, err := ParseMaskWriteRegister(adu.Data())
		if err != nil {
			return nil, err
		}
		if err := d.maskData(
			DataTypeTypeHoldingRegisters, addr, and, or,
		); err != nil {
			return nil, err
		}
		return adu.Data(), nil
	case FunctionReadWriteMultipleRegisters:
		raddr, nr, waddr, v, err := ParseReadWriteMultipleRegisters(adu.Data())
		if err != nil {
			return nil, err
		}
		response := make([]byte, 1, 2*nr+1)
		response[0] = byte(2 * nr)
		if err := d.writeReadData(
			response, DataTypeHoldingRegisters, waddr, v, raddr, nr,
		); err != nil {
			return nil, err
		}
		return response, nil
	default:
		return nil, ExceptionIllegalFunction
	}
}