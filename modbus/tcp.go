package modbus

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	// defaultTCPAddr is the default listening address for the MBAP protocol.
	defaultTCPAddr = "127.0.0.1:502"

	// defaultTimeout is the default request timeout.
	// Since we allow multiple parallel connections, i. e., a crashed client can
	// reconnect immediately after a restart, the considerations in § 4.2.2.3 of
	// the Modbus TCP/IP messaging implementation guide do not apply and we can
	// afford a longer timeout. The user can still set a custom timeout using
	// WithTCPTimeout.
	defaultTimeout = 75 * time.Second
)

// mbap is the Modbus application protocol header.
type mbap [7]byte

// Validate validates this MBAP.
func (m *mbap) Validate() error {
	// Check protocol identifier
	if m[2] != 0 || m[3] != 0 {
		return errors.New("bad protocol identifier")
	}
	// Check length (encoded length is PDU length + 1 byte for the unit
	// identifier).
	l := binary.BigEndian.Uint16(m[4:6])
	if l < minPDULen+1 || l > maxPDULen+1 {
		return errors.New("bad length")
	}
	return nil
}

// PDULen returns the PDU length encoded in this MBAP.
// Assumes a valid MBAP.
func (m *mbap) PDULen() int {
	return int(binary.BigEndian.Uint16(m[4:6])) - 1 // subtract unit id byte
}

// SetPDULen sets the PDU length in this MBAP. The specified size is not checked
// for validity.
func (m *mbap) SetPDULen(size int) {
	binary.BigEndian.PutUint16(m[4:6], uint16(size+1))
}

// tcpAddress describes a TCP address.
type tcpAddress struct {
	// protocol is the Modbus protocol used (mbap or mbaps).
	protocol string

	// addr is the underlying TCP address.
	underlying net.Addr
}

// Protocol implements Address.
func (addr *tcpAddress) Protocol() string {
	return addr.protocol
}

// String implements Address.
func (addr *tcpAddress) String() string {
	return fmt.Sprintf("%s://%s", addr.protocol, addr.underlying)
}

// tcpMessage describes a TCP message.
type tcpMessage struct {
	// tlsConfig is the TLS configuration based on which this message was sent.
	// If no TLS was used, this is nil.
	tlsConfig *tls.Config

	// from and to are the origin and destination of this message, respectively.
	from, to net.Addr

	// mbapHeader is the mbapHeader of this message.
	mbapHeader mbap

	// data is the message data, including the function code.
	data []byte
}

// protocol returns the protocol over which this message was sent.
func (m *tcpMessage) protocol() string {
	if m.tlsConfig == nil {
		return "mbap"
	}
	return "mbaps"
}

// From implements Message.
func (m *tcpMessage) From() Address {
	return &tcpAddress{
		protocol:   m.protocol(),
		underlying: m.from,
	}
}

// To implements Message.
func (m *tcpMessage) To() Address {
	return &tcpAddress{
		protocol:   m.protocol(),
		underlying: m.to,
	}
}

// ADU implements Message.
func (m *tcpMessage) ADU() ADU {
	return m
}

// UnitID implements ADU.
func (m *tcpMessage) UnitID() UnitID {
	return UnitID(m.mbapHeader[6])
}

// Function implements ADU.
func (m *tcpMessage) Function() FunctionCode {
	return FunctionCode(m.data[0])
}

// Data implements ADU.
func (m *tcpMessage) Data() []byte {
	return m.data[1:]
}

// tcpOptions describes options for Modbus/TCP servers.
type tcpOptions struct {
	// addr is the local address the TCP listener should listen on.
	addr string

	// timeout is the request timeout.
	timeout time.Duration

	// insecure determines whether the listener is permitted to be insecure.
	insecure bool

	// tlsConfig is the server TLS configuration.
	tlsConfig *tls.Config
}

// Validate performs cursory validation of these TCP options.
// It also fills in default values where appropriate.
func (opt *tcpOptions) Validate() error {
	if opt.tlsConfig == nil && !opt.insecure {
		return errors.New("need WithInsecure() option for insecure operation")
	}
	if opt.addr == "" {
		opt.addr = defaultTCPAddr
	}
	if opt.timeout == 0 {
		opt.timeout = defaultTimeout
	}
	return nil
}

// TCPOption describes an option to be passed to ListenTCP.
type TCPOption func(*tcpOptions) error

// WithListenAddress instructs ListenTCP to use the specified local
// TCP address to listen on.
func WithListenAddress(addr string) TCPOption {
	return func(opt *tcpOptions) error {
		if opt.addr != "" {
			return errors.New("duplicate specification of listen address")
		}
		if addr == "" {
			return errors.New("empty listen address")
		}
		opt.addr = addr
		return nil
	}
}

// WithTCPTimeout selects the request timeout to be used for a TCP connection.
// The timeout is applied in two situations. First, if the client does not send
// a request for the given duration, the connection is closed. Second, the
// processing time of the request itself is limited by timeout. If it is
// exceeded, ExceptionServerDeviceBusy will be sent back to the client.
func WithTCPTimeout(timeout time.Duration) TCPOption {
	return func(opt *tcpOptions) error {
		if timeout <= 0 {
			return fmt.Errorf("timeout must be positive, got %s", timeout)
		}
		if opt.timeout != 0 {
			return errors.New("WithTCPTimeout specified multiple times")
		}
		opt.timeout = timeout
		return nil
	}
}

// WithInsecure instructs ListenTCP to use the insecure mbap protocol.
func WithInsecure() TCPOption {
	return func(opt *tcpOptions) error {
		if opt.tlsConfig != nil {
			return errors.New("cannot use WithInsecure together with TLS config")
		}
		opt.insecure = true
		return nil
	}
}

// tcpListener describes a Modbus/TCP listener (optionally secure).
type tcpListener struct {
	// underlying is the underlying net.Listener.
	underlying net.Listener

	// timeout is the connection read/write timeout.
	timeout time.Duration

	// activeConns keeps track of the active connections for this listener.
	activeConns sync.WaitGroup

	// closed is a sentry channel which will be closed when this listener is
	// closed.
	closed chan struct{}
}

// ListenTCP creates a mbap or mbaps TCP listener and forwards all incoming
// requests to the given server.
func ListenTCP(srv *Server, opts ...TCPOption) (Listener, error) {
	localOpts := &tcpOptions{}
	for _, opt := range opts {
		if err := opt(localOpts); err != nil {
			return nil, err
		}
	}
	if err := localOpts.Validate(); err != nil {
		return nil, err
	}
	result := &tcpListener{
		timeout: localOpts.timeout,
		closed:  make(chan struct{}),
	}
	var err error
	result.underlying, err = net.Listen("tcp", localOpts.addr)
	if err != nil {
		return nil, fmt.Errorf("listen on tcp socket '%s': %w", localOpts.addr, err)
	}
	go result.handleConnections(srv)
	return result, nil
}

// Close closes this listener.
func (l *tcpListener) Close() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("already closed")
		}
		l.activeConns.Wait()
	}()
	err = l.underlying.Close()
	close(l.closed)
	return
}

// handleConnections handles incoming connections for this listener.
func (l *tcpListener) handleConnections(srv *Server) {
	for {
		conn, err := l.underlying.Accept()
		if err != nil {
			return
		}
		l.activeConns.Add(1)
		go l.handleConnection(srv, conn)
	}
}

// handleConnection handles an incoming connection for this listener.
// It will serve requests until either this listener is closed, there
// is an error on the connection, or a timeout occurs.
func (l *tcpListener) handleConnection(srv *Server, conn net.Conn) {
	defer l.activeConns.Done()
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.serveRequests(srv, conn)
	}()
	select {
	case <-l.closed:
	case <-done:
	}
}

// serveRequests serves incoming requests on the specified connection to
// the specified Modbus server.
func (l *tcpListener) serveRequests(srv *Server, conn net.Conn) {
	r := bufio.NewReaderSize(conn, 320)
	w := bufio.NewWriterSize(conn, 320)
	var (
		mbapHeader mbap
		data       = make([]byte, 256)
	)
	for {
		// Read request
		deadline := time.Now().Add(l.timeout)
		if err := conn.SetReadDeadline(deadline); err != nil {
			return
		}
		if _, err := io.ReadFull(r, mbapHeader[:]); err != nil {
			return
		}
		if err := mbapHeader.Validate(); err != nil {
			return
		}
		data = data[:mbapHeader.PDULen()]
		if _, err := io.ReadFull(r, data); err != nil {
			return
		}
		// Process request
		deadline = time.Now().Add(l.timeout)
		msg := l.makeMessage(conn, &mbapHeader, data)
		fc := msg.Function()
		response, answer, exception := l.sendRequest(srv, msg, deadline)
		if !answer {
			continue
		}
		if response == nil {
			fc = fc.AsError()
			response = []byte{byte(exception)}
		}
		// Send response
		mbapHeader.SetPDULen(len(response) + 1) // add 1 for function code
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return
		}
		if _, err := w.Write(mbapHeader[:]); err != nil {
			return
		}
		if _, err := w.Write([]byte{byte(fc)}); err != nil {
			return
		}
		if _, err := w.Write(response); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}

// makeMessage assembles a TCP message,
func (l *tcpListener) makeMessage(
	conn net.Conn, mbapHeader *mbap, data []byte,
) *tcpMessage {
	return &tcpMessage{
		from:       conn.RemoteAddr(),
		to:         conn.LocalAddr(),
		mbapHeader: *mbapHeader,
		data:       data,
	}
}

// sendRequest sends the request message msg to the given server.
// This method will give up waiting for an answer once the given deadline is
// exceeded.
func (l *tcpListener) sendRequest(
	srv *Server, msg Message, deadline time.Time,
) (response []byte, answer bool, exception ExceptionCode) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		answer = true
		resp, err := srv.Request(ctx, msg)
		if err != nil {
			var ok bool
			exception, ok = err.(ExceptionCode)
			if !ok {
				exception = ExceptionServerDeviceFailure
			}
			return
		}
		if resp == nil {
			answer = false
			return
		}
		response = resp
	}()
	select {
	case <-done:
		return
	case <-ctx.Done():
		return nil, true, ExceptionServerDeviceBusy
	}
}
