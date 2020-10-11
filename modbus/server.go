package modbus

import (
	"context"
	"fmt"
	"sync"
)

// unitAndFunction combines unit identifier and function code.
type unitAndFunction struct {
	// unitID is the Modbus unit identifier.
	unitID UnitID

	// functionCode is the Modbus function code.
	functionCode FunctionCode
}

// FunctionHandler is the handler function type to handle Modbus functions.
// The handler should return the response data (without the function code) on
// success. On error, the returned error should normally be an Exception. If it
// is not an exception, the server frontend should respond with
// ExceptionServerDeviceFailure.
//
// A Server may invoke multiple handlers concurrently. Therefore, handlers are
// responsible for protecting shared resources from concurrent access.
type FunctionHandler func(
	ctx context.Context, request Message, srv *Server,
) ([]byte, error)

// Server describes a Modbus server.
type Server struct {
	// mx protects direct access to the server fields.
	mx sync.RWMutex

	// functionHandlers maps unit ID and function code to their handler.
	functionHandlers map[unitAndFunction]FunctionHandler

	// fallbackFunctionHandler is used for unit-function-combinations not in
	// functionHandlers.
	fallbackFunctionHandler FunctionHandler
}

// defaultFunctionHandler is the initial fallback handler for Modbus functions
// used by servers returned by NewServer. It simply returns
// ExceptionIllegalFunction.
func defaultFunctionHandler(context.Context, Message, *Server) ([]byte, error) {
	return nil, ExceptionIllegalFunction
}

// NewServer returns a new server.
// Initially, the response to all incoming requests would be
// ExceptionIllegalFunction.
func NewServer() *Server {
	return &Server{
		functionHandlers:        make(map[unitAndFunction]FunctionHandler),
		fallbackFunctionHandler: defaultFunctionHandler,
	}
}

// SetFallbackFunctionHandler sets the function handler to be called by this
// server for incoming requests without a specific function handler set by
// s.SetFunctionHandler. If the argument is nil, a default handler, which
// simply returns ExceptionIllegalFunction for all requests, will be used.
func (s *Server) SetFallbackFunctionHandler(h FunctionHandler) {
	if h == nil {
		h = defaultFunctionHandler
	}
	s.mx.Lock()
	defer s.mx.Unlock()
	s.fallbackFunctionHandler = h
}

// SetFunctionHandler sets a function handler in this server for the specified
// unit and functions. If the given handler is nil, any existing handlers at
// the specified unit and functions will be deleted instead. Further requests
// matching the unit and functions will use the fallback handler instead.
//
// The given function codes must not be function error codes, but otherwise,
// this implementation accepts all function codes, including reserved function
// codes and the zero function code.
func (s *Server) SetFunctionHandler(
	h FunctionHandler, unit UnitID, functions ...FunctionCode,
) error {
	key := unitAndFunction{
		unitID: unit,
	}
	s.mx.Lock()
	defer s.mx.Unlock()
	if h == nil {
		for _, key.functionCode = range functions {
			delete(s.functionHandlers, key)
		}
		return nil
	}
	// Check for collisions and illegal values first, and then add the new
	// handlers.
	for _, key.functionCode = range functions {
		if key.functionCode.IsError() {
			return fmt.Errorf("error function code %d not permitted",
				key.functionCode)
		}
		if s.functionHandlers[key] != nil {
			return fmt.Errorf(
				"handler for unit %d and function code %d already present",
				key.unitID, key.functionCode)
		}
	}
	for _, key.functionCode = range functions {
		s.functionHandlers[key] = h
	}
	return nil
}

// Request performs a low-level request on this server. It is used by
// implementations of higher-level functionality, such as the
// Modbus memory model. Users should rarely need to use this method directly.
//
// The returned byte slice is the response data without the function code.
//
// If an error occurs, the error is usually an ExceptionCode, but handlers are
// allowed to return other errors as well, which the caller may choose to handle
// specially (e. g., by closing a connection), or simply return
// ExceptionServerDeviceFailure to the client.
func (s *Server) Request(ctx context.Context, msg Message) ([]byte, error) {
	if msg == nil {
		panic("nil request message")
	}
	adu := msg.ADU()
	if adu == nil {
		panic("nil request ADU")
	}
	uid := adu.UnitID()
	fid := adu.Function()
	s.mx.RLock()
	h := s.functionHandlers[unitAndFunction{uid, fid}]
	if h == nil {
		h = s.fallbackFunctionHandler
	}
	s.mx.RUnlock()
	return h(ctx, msg, s)
}
