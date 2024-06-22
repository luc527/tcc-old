package main

// I think this Go implementation might end up being the reference implementation

import (
	"bytes"
	"encoding/binary"
	"io"
	"log"
	"log/slog"
	"net"
)

type mtype byte // message type

const (
	// types of incoming messages
	mExit  = mtype(0b000)
	mEnter = mtype(0b001)
	mSend  = mtype(0b011)

	// types of outgoing messages
	mRecv = mtype(0b010)
	mSig  = mtype(0xFF)
)

// instead of 2 bytes for a signal,
// could've used just the first bit of the byte to show it's a signal
// and the remaining 7 bits for the signal code

// outgoing message
// mtype always mRecv
type mes struct {
	p uint32 // port
	b []byte // body
}

// outgoing signal
// mtype always mSig
type sig byte

const (
	sOkEnter = sig(0x01)
	sOkExit  = sig(0x02)
	sOkSend  = sig(0x03)
	sErrType = sig(0x80)
)

// TODO instead of an error type
// let's include a signal type
// which includes errors (when the signal has a 1 in the most significant bit)
// and things like.. entered successfully, exited successfully, sent successfully

func serve() {
	listener, err := net.Listen("tcp", "localhost:1703")
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("connection error: %v\n", err)
		} else {
			go handle(conn)
		}
	}
}

func handle(conn io.ReadWriteCloser) {
	defer conn.Close()

	mc := make(chan mes) // channel for outgoing messages
	sc := make(chan sig) // channel for outgoing signals

	defer close(mc)
	defer close(sc)

	go cliWriter(conn, mc, sc)
	cliReader(conn, mc, sc)
}

func readFull(r io.Reader, buf []byte) error {
	rem := buf
	for len(rem) > 0 {
		n, err := r.Read(rem)
		if err != nil {
			return err
		}
		rem = rem[n:]
	}
	return nil
}

func cliReader(r io.Reader, mc chan<- mes, sc chan<- sig) {
	// TODO the "main" connection goroutine reads from the client
	// but I should start a goroutine for sending messages back instead of doing it in the same goroutine
	// and pass the channel for the internal handlers to send to
	// except handleSend which will also take the conn as an io.Reader to read the body
	// -- but then caution
	// with two goroutines whould detect closing from both sides
	// without leaking goroutines and without deadlocks

	for {
		var tbuf [1]byte
		if err := readFull(r, tbuf[:]); err != nil {
			if err != io.EOF {
				slog.Error("handle: read type", "error", err)
			}
			break
		}
		t := mtype(tbuf[0])

		var portbuf [4]byte
		if err := readFull(r, portbuf[:]); err != nil {
			if err != io.EOF {
				slog.Error("handle: read port", "error", err)
			}

		}
		port := binary.LittleEndian.Uint32(portbuf[:])

		if t == mEnter {
			handleEnter(port, sc)
		} else if t == mExit {
			handleExit(port, sc)
		} else if t == mSend {
			// the reader might EOF in this call
			// but we'll leave it for the next iteration of the loop to detect that
			handleSend(port, sc, r)
		} else {
			handleInvalidType(sc)
		}
	}
}

func cliWriter(w io.Writer, mc <-chan mes, sc <-chan sig) {
	// TODO how to deal correctly with errors on writing?

	for {
		select {
		case m, ok := <-mc:
			if !ok {
				break
			}
			t := [1]byte{byte(mRecv)}
			port := [4]byte{}
			binary.LittleEndian.PutUint32(port[:], m.p)
			if _, err := w.Write(t[:]); err != nil {
				log.Printf("cli writer message type: %v", err)
			} else if _, err := w.Write(port[:]); err != nil {
				log.Printf("cli writer message port: %v", err)
			} else if _, err := w.Write(m.b); err != nil {
				log.Printf("cli writer message body: %v", err)
			}
		case s, ok := <-sc:
			if !ok {
				break
			}
			sig := [2]byte{
				byte(mSig),
				byte(s),
			}
			if _, err := w.Write(sig[:]); err != nil {
				log.Printf("cli writer signal %v", err)
			}
		}
	}
}

func handleEnter(port uint32, sc chan<- sig) {
	log.Printf("enter %d\n", port)
	sc <- sOkEnter
}

func handleExit(port uint32, sc chan<- sig) {
	log.Printf("exit %d\n", port)
	sc <- sOkExit
}

func readBody(r io.Reader) ([]byte, error) {
	var sizbuf [2]byte
	if err := readFull(r, sizbuf[:]); err != nil {
		return nil, err
	}
	siz := binary.LittleEndian.Uint16(sizbuf[:])

	// this is nice
	b := new(bytes.Buffer)
	io.Copy(b, io.LimitReader(r, int64(siz)))
	return b.Bytes(), nil
}

func handleSend(port uint32, sc chan<- sig, r io.Reader) {
	bs, err := readBody(r)
	if err != nil {
		if err != io.EOF {
			log.Printf("handle send: %v", err)
		}
		return
	}
	log.Printf("send %q to %d\n", string(bs), port)
	sc <- sOkSend
}

func handleInvalidType(sc chan<- sig) {
	log.Printf("invalid\n")
	sc <- sErrType
}

func main() {
	serve()
}
