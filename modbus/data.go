package modbus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/TheCount/go-multilocker/multilocker"
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

// IsReadOnly returns true if and only if this data type is read-only for the
// Modbus client (i. e., discrete inputs or input registers).
func (dt DataType) IsReadOnly() bool {
	return dt == DataTypeDiscreteInputs || dt == DataTypeInputRegisters
}

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

// getMutexen returns all mutexes necessary for the specified list of
// references.
func (*Data) getMutexen(refs []dataRef) []*sync.RWMutex {
	blocks := make(map[*dataBlock]struct{})
	result := make([]*sync.RWMutex, 0, len(refs))
	for _, ref := range refs {
		if _, ok := blocks[ref.block]; !ok {
			blocks[ref.block] = struct{}{}
			result = append(result, &ref.block.mx)
		}
	}
	return result
}

// getNeededRefs returns a list of needed references for the specified range
// in the specified data type.
func (d *Data) getNeededRefs(dt DataType, start, count int) ([]dataRef, error) {
	result := d.refs[dt]
	// strip start
	for {
		if len(result) == 0 {
			return nil, ExceptionIllegalDataAddress
		}
		if result[0].startBit+result[0].numBits > start {
			break
		}
		result = result[1:]
	}
	// strip end
	for {
		lastidx := len(result) - 1
		if result[lastidx].startBit < start+count {
			break
		}
		if lastidx == 0 {
			return nil, ExceptionIllegalDataAddress
		}
		result = result[:lastidx]
	}
	// Make sure there are no gaps
	for i := 0; i < len(result)-1; i++ {
		if result[i].startBit+result[i].numBits != result[i+1].startBit {
			return nil, ExceptionIllegalDataAddress
		}
	}
	return result, nil
}

// getReadLocker returns a read locker for the specified list of data
// references. The returned locker atomically locks all specified references.
func (d *Data) getReadLocker(refs []dataRef) sync.Locker {
	mxs := d.getMutexen(refs)
	lockers := make([]sync.Locker, len(mxs))
	for i := range lockers {
		lockers[i] = mxs[i].RLocker()
	}
	return multilocker.New(lockers...)
}

// getWriteLocker returns a write locker for the specified list of data
// references. The returned locker atomically locks all specified references.
func (d *Data) getWriteLocker(refs []dataRef) sync.Locker {
	mxs := d.getMutexen(refs)
	lockers := make([]sync.Locker, len(mxs))
	for i := range lockers {
		lockers[i] = mxs[i]
	}
	return multilocker.New(lockers...)
}

// readData reads n data items for the specified data type from the specified
// address and appends them to dst.
func (d *Data) readData(
	dst []byte, dt DataType, addr uint16, n int,
) ([]byte, error) {
	startBit := numBits[dt] * int(addr)
	count := numBits[dt] * n
	refs, err := d.getNeededRefs(dt, startBit, count)
	if err != nil {
		return nil, err
	}
	ml := d.getReadLocker(refs)
	ml.Lock()
	defer ml.Unlock()
	// copy bit by bit
	// FIXME: We could make this faster in many common special cases
	spill := 0
	var current *byte
	for _, ref := range refs {
		for startBit < ref.startBit+ref.numBits && count > 0 {
			if spill == 0 {
				dst = append(dst, 0)
				current = &dst[len(dst)-1]
				spill = 8
			}
			blockBit := ref.blockOffset - ref.startBit + startBit
			blockByte, blockOffset := blockBit/8, blockBit%8
			*current |=
				((ref.block.data[blockByte] >> blockOffset) & 1) << (8 - spill)
			startBit++
			count--
			spill--
		}
	}
	return dst, nil
}

// writeData writes n data itens for the specified data type to the specified
// address from src.
func (d *Data) writeData(dt DataType, addr uint16, n int, src []byte) error {
	startBit := numBits[dt] * int(addr)
	count := numBits[dt] * n
	refs, err := d.getNeededRefs(dt, startBit, count)
	if err != nil {
		return err
	}
	ml := d.getWriteLocker(refs)
	ml.Lock()
	defer ml.Unlock()
	// copy bit by bit
	// FIXME: We could make this faster in many common special cases
	srcBit := 0
	for _, ref := range refs {
		for startBit < ref.startBit+ref.numBits && count > 0 {
			if srcBit == 8 {
				src = src[1:]
				srcBit = 0
			}
			blockBit := ref.blockOffset - ref.startBit + startBit
			blockByte, blockOffset := blockBit/8, blockBit%8
			ref.block.data[blockByte] &^= 1 << blockOffset
			ref.block.data[blockByte] |= ((src[0] >> srcBit) & 1) << blockOffset
			startBit++
			count--
			srcBit++
		}
	}
	return nil
}

// maskData performs a Modbus mask data operation on a single word.
func (d *Data) maskData(dt DataType, addr, and, or uint16) error {
	startBit := 16 * int(addr)
	refs, err := d.getNeededRefs(dt, startBit, 16)
	if err != nil {
		return err
	}
	if len(refs) != 1 {
		// FIXME: overlapping data blocks not supported for this operation
		return ExceptionIllegalDataAddress
	}
	ref := refs[0]
	ref.block.mx.Lock()
	defer ref.block.mx.Unlock()
	blockByte := (ref.blockOffset - ref.startBit + startBit) / 8
	data := ref.block.data[blockByte : blockByte+2]
	word := binary.BigEndian.Uint16(data)
	word = (word & and) | (or &^ and)
	binary.BigEndian.PutUint16(data, word)
	return nil
}

// writeReadData atomically writes the specified values and reads n data items,
// appending them to dest.
func (d *Data) writeReadData(
	dst []byte, dt DataType,
	writeAddr uint16, src []byte, readAddr uint16, n int,
) ([]byte, error) {
	writeStartBit := 16 * int(writeAddr)
	writeCount := 8 * len(src)
	readStartBit := 16 * int(readAddr)
	readCount := 16 * n
	writeRefs, err := d.getNeededRefs(dt, writeStartBit, writeCount)
	if err != nil {
		return nil, err
	}
	readRefs, err := d.getNeededRefs(dt, readStartBit, readCount)
	if err != nil {
		return nil, err
	}
	combinedRefs := make([]dataRef, len(writeRefs)+len(readRefs))
	copy(combinedRefs, writeRefs)
	copy(combinedRefs[len(writeRefs):], readRefs)
	ml := d.getWriteLocker(combinedRefs)
	ml.Lock()
	defer ml.Unlock()
	// copy bit by bit
	// FIXME: We could make this faster in many common special cases
	srcBit := 0
	for _, ref := range writeRefs {
		for writeStartBit < ref.startBit+ref.numBits && writeCount > 0 {
			if srcBit == 8 {
				src = src[1:]
				srcBit = 0
			}
			blockBit := ref.blockOffset - ref.startBit + writeStartBit
			blockByte, blockOffset := blockBit/8, blockBit%8
			ref.block.data[blockByte] &^= 1 << blockOffset
			ref.block.data[blockByte] |= ((src[0] >> srcBit) & 1) << blockOffset
			writeStartBit++
			writeCount--
			srcBit++
		}
	}
	// copy bit by bit
	// FIXME: We could make this faster in many common special cases
	spill := 0
	var current *byte
	for _, ref := range readRefs {
		for readStartBit < ref.startBit+ref.numBits && readCount > 0 {
			if spill == 0 {
				dst = append(dst, 0)
				current = &dst[len(dst)-1]
				spill = 8
			}
			blockBit := ref.blockOffset - ref.startBit + readStartBit
			blockByte, blockOffset := blockBit/8, blockBit%8
			*current |=
				((ref.block.data[blockByte] >> blockOffset) & 1) << (8 - spill)
			readStartBit++
			readCount--
			spill--
		}
	}
	return dst, nil
}

// FunctionHandler is the Modbus function handler for the specified data.
func (d *Data) FunctionHandler(
	ctx context.Context, request Message, srv *Server,
) ([]byte, error) {
	var p Parser
	adu := request.ADU()
	switch adu.Function() {
	case FunctionReadCoils:
		addr, n, err := p.ParseReadCoils(adu.Data())
		if err != nil {
			return nil, err
		}
		response := make([]byte, 1, n/8+2)
		response[0] = byte((n + 7) / 8)
		return d.readData(response, DataTypeCoils, addr, n)
	case FunctionReadDiscreteInputs:
		addr, n, err := p.ParseReadDiscreteInputs(adu.Data())
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
		addr, n, err := p.ParseReadHoldingRegisters(adu.Data())
		if err != nil {
			return nil, err
		}
		response := make([]byte, 1, 2*n+1)
		response[0] = byte(2 * n)
		return d.readData(response, DataTypeHoldingRegisters, addr, n)
	case FunctionReadInputRegisters:
		addr, n, err := p.ParseReadInputRegisters(adu.Data())
		if err != nil {
			return nil, err
		}
		response := make([]byte, 1, 2*n+1)
		response[0] = byte(2 * n)
		return d.readData(response, DataTypeInputRegisters, addr, n)
	case FunctionWriteSingleCoil:
		addr, v, err := p.ParseWriteSingleCoil(adu.Data())
		if err != nil {
			return nil, err
		}
		wData := []byte{0}
		if v {
			wData[0] = 1
		}
		if err := d.writeData(DataTypeCoils, addr, 1, wData); err != nil {
			return nil, err
		}
		return adu.Data(), nil
	case FunctionWriteSingleRegister:
		addr, v, err := p.ParseWriteSingleRegister(adu.Data())
		if err != nil {
			return nil, err
		}
		wData := make([]byte, 2)
		binary.BigEndian.PutUint16(wData, uint16(v))
		if err := d.writeData(
			DataTypeHoldingRegisters, addr, 1, wData,
		); err != nil {
			return nil, err
		}
		return adu.Data(), nil
	case FunctionWriteMultipleCoils:
		addr, n, v, err := p.ParseWriteMultipleCoils(adu.Data())
		if err != nil {
			return nil, err
		}
		if err := d.writeData(DataTypeCoils, addr, n, v); err != nil {
			return nil, err
		}
		return adu.Data()[:4], nil
	case FunctionWriteMultipleRegisters:
		addr, v, err := p.ParseWriteMultipleRegisters(adu.Data())
		if err != nil {
			return nil, err
		}
		if err := d.writeData(
			DataTypeHoldingRegisters, addr, len(v), v,
		); err != nil {
			return nil, err
		}
		return adu.Data()[:4], nil
	case FunctionMaskWriteRegister:
		addr, and, or, err := p.ParseMaskWriteRegister(adu.Data())
		if err != nil {
			return nil, err
		}
		if err := d.maskData(
			DataTypeHoldingRegisters, addr, and, or,
		); err != nil {
			return nil, err
		}
		return adu.Data(), nil
	case FunctionReadWriteMultipleRegisters:
		raddr, nr, waddr, v, err := p.ParseReadWriteMultipleRegisters(adu.Data())
		if err != nil {
			return nil, err
		}
		response := make([]byte, 1, 2*nr+1)
		response[0] = byte(2 * nr)
		return d.writeReadData(response, DataTypeHoldingRegisters,
			waddr, v, raddr, nr)
	default:
		return nil, ExceptionIllegalFunction
	}
}

// SetInt16 is a convenience function which sets the register specified by
// dt and addr to the specified 2's complement signed 16-bit integer value.
func (d *Data) SetInt16(dt DataType, addr uint16, value int16) error {
	var buf [2]byte
	buf[0] = byte(uint16(value) >> 8)
	buf[1] = byte(uint16(value) >> 0)
	return d.writeData(dt, addr, 16/numBits[dt], buf[:])
}

// SetUint16 is a convenience function which sets the register specified by
// dt and addr to the specified unsigned 16-bit integer value.
func (d *Data) SetUint16(dt DataType, addr uint16, value uint16) error {
	var buf [2]byte
	buf[0] = byte(value >> 8)
	buf[1] = byte(value >> 0)
	return d.writeData(dt, addr, 16/numBits[dt], buf[:])
}

// SetInt32BE is a convenience function which sets the registers specified by
// dt and addr to the specified 2's complement signed 32-bit integer value
// in big endian order.
func (d *Data) SetInt32BE(dt DataType, addr uint16, value int32) error {
	var buf [4]byte
	buf[0] = byte(uint32(value) >> 24)
	buf[1] = byte(uint32(value) >> 16)
	buf[2] = byte(uint32(value) >> 8)
	buf[3] = byte(uint32(value) >> 0)
	return d.writeData(dt, addr, 32/numBits[dt], buf[:])
}

// SetInt32LE is a convenience function which sets the registers specified by
// dt and addr to the specified 2's complement signed 32-bit integer value
// in little endian order.
func (d *Data) SetInt32LE(dt DataType, addr uint16, value int32) error {
	var buf [4]byte
	buf[2] = byte(uint32(value) >> 24)
	buf[3] = byte(uint32(value) >> 16)
	buf[0] = byte(uint32(value) >> 8)
	buf[1] = byte(uint32(value) >> 0)
	return d.writeData(dt, addr, 32/numBits[dt], buf[:])
}

// SetUint32BE is a convenience function which sets the registers specified by
// dt and addr to the specified unsigned 32-bit integer value
// in big endian order.
func (d *Data) SetUint32BE(dt DataType, addr uint16, value uint32) error {
	var buf [4]byte
	buf[0] = byte(value >> 24)
	buf[1] = byte(value >> 16)
	buf[2] = byte(value >> 8)
	buf[3] = byte(value >> 0)
	return d.writeData(dt, addr, 32/numBits[dt], buf[:])
}

// SetUint32LE is a convenience function which sets the registers specified by
// dt and addr to the specified unsigned 32-bit integer value
// in little endian order.
func (d *Data) SetUint32LE(dt DataType, addr uint16, value uint32) error {
	var buf [4]byte
	buf[2] = byte(value >> 24)
	buf[3] = byte(value >> 16)
	buf[0] = byte(value >> 8)
	buf[1] = byte(value >> 0)
	return d.writeData(dt, addr, 32/numBits[dt], buf[:])
}

// SetInt64BE is a convenience function which sets the registers specified by
// dt and addr to the specified 2's complement signed 64-bit integer value
// in big endian order.
func (d *Data) SetInt64BE(dt DataType, addr uint16, value int64) error {
	var buf [8]byte
	buf[0] = byte(uint64(value) >> 56)
	buf[1] = byte(uint64(value) >> 48)
	buf[2] = byte(uint64(value) >> 40)
	buf[3] = byte(uint64(value) >> 32)
	buf[4] = byte(uint64(value) >> 24)
	buf[5] = byte(uint64(value) >> 16)
	buf[6] = byte(uint64(value) >> 8)
	buf[7] = byte(uint64(value) >> 0)
	return d.writeData(dt, addr, 64/numBits[dt], buf[:])
}

// SetInt64LE is a convenience function which sets the registers specified by
// dt and addr to the specified 2's complement signed 64-bit integer value
// in little endian order.
func (d *Data) SetInt64LE(dt DataType, addr uint16, value int64) error {
	var buf [8]byte
	buf[6] = byte(uint64(value) >> 56)
	buf[7] = byte(uint64(value) >> 48)
	buf[4] = byte(uint64(value) >> 40)
	buf[5] = byte(uint64(value) >> 32)
	buf[2] = byte(uint64(value) >> 24)
	buf[3] = byte(uint64(value) >> 16)
	buf[0] = byte(uint64(value) >> 8)
	buf[1] = byte(uint64(value) >> 0)
	return d.writeData(dt, addr, 64/numBits[dt], buf[:])
}

// SetUint64BE is a convenience function which sets the registers specified by
// dt and addr to the specified unsigned 64-bit integer value
// in big endian order.
func (d *Data) SetUint64BE(dt DataType, addr uint16, value uint64) error {
	var buf [8]byte
	buf[0] = byte(value >> 56)
	buf[1] = byte(value >> 48)
	buf[2] = byte(value >> 40)
	buf[3] = byte(value >> 32)
	buf[4] = byte(value >> 24)
	buf[5] = byte(value >> 16)
	buf[6] = byte(value >> 8)
	buf[7] = byte(value >> 0)
	return d.writeData(dt, addr, 64/numBits[dt], buf[:])
}

// SetUint64LE is a convenience function which sets the registers specified by
// dt and addr to the specified unsigned 64-bit integer value
// in little endian order.
func (d *Data) SetUint64LE(dt DataType, addr uint16, value uint64) error {
	var buf [8]byte
	buf[6] = byte(value >> 56)
	buf[7] = byte(value >> 48)
	buf[4] = byte(value >> 40)
	buf[5] = byte(value >> 32)
	buf[2] = byte(value >> 24)
	buf[3] = byte(value >> 16)
	buf[0] = byte(value >> 8)
	buf[1] = byte(value >> 0)
	return d.writeData(dt, addr, 64/numBits[dt], buf[:])
}

// SetFloat32BE is a convenience function which sets the registers specified by
// dt and addr to the specified single-precision floating point value
// in big endian order.
func (d *Data) SetFloat32BE(dt DataType, addr uint16, value float32) error {
	return d.SetUint32BE(dt, addr, math.Float32bits(value))
}

// SetFloat32LE is a convenience function which sets the registers specified by
// dt and addr to the specified single-precision floating point value
// in little endian order.
func (d *Data) SetFloat32LE(dt DataType, addr uint16, value float32) error {
	return d.SetUint32LE(dt, addr, math.Float32bits(value))
}

// SetFloat64BE is a convenience function which sets the registers specified by
// dt and addr to the specified double-precision floating point value
// in big endian order.
func (d *Data) SetFloat64BE(dt DataType, addr uint16, value float64) error {
	return d.SetUint64BE(dt, addr, math.Float64bits(value))
}

// SetFloat64LE is a convenience function which sets the registers specified by
// dt and addr to the specified double-precision floating point value
// in little endian order.
func (d *Data) SetFloat64LE(dt DataType, addr uint16, value float64) error {
	return d.SetUint64LE(dt, addr, math.Float64bits(value))
}

// SetStringBE sets the specified number of addresses to the specified string
// in big-endian order. Excess characters are stripped, missing characters are
// filled with zeroes.
func (d *Data) SetStringBE(
	dt DataType, addr uint16, len int, value string,
) error {
	numBytes := (len*numBits[dt] + 7) / 8
	buf := make([]byte, numBytes)
	copy(buf, value)
	return d.writeData(dt, addr, len, buf)
}

// SetStringLE sets the specified number of addresses to the specified string
// in little-endian order. Excess characters are stripped, missing characters
// are filled with zeroes.
func (d *Data) SetStringLE(
	dt DataType, addr uint16, len int, value string,
) error {
	numBytes := (len*numBits[dt] + 7) / 8
	buf := make([]byte, numBytes)
	copy(buf, value)
	for i := 0; i < numBytes; i += 2 {
		buf[i], buf[i+1] = buf[i+1], buf[i]
	}
	return d.writeData(dt, addr, len, buf)
}
