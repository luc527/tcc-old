package main

// I think this Go implementation might end up being the reference implementation

import (
	"bytes"
	"encoding/binary"
	"io"
	"log"
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

func serve() {
	listener, err := net.Listen("tcp", "localhost:1703")
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("connection error: %v", err)
		} else {
			go handle(conn)
		}
	}
}

func handle(conn io.ReadWriteCloser) {
	defer conn.Close()

	mc := make(chan mes) // channel for outgoing messages
	sc := make(chan sig) // channel for outgoing signals

	go cliWriter(conn, mc, sc) // read from the channels and write to the conn
	cliReader(conn, mc, sc)    // read from the conn and write to the channels (now or later when a message from another user is broadcasted)
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
	defer close(mc)
	defer close(sc)

	for {
		var headbuf [5]byte
		if err := readFull(r, headbuf[:]); err != nil {
			if err != io.EOF {
				log.Printf("handle read head: %v", err)
			}
			break
		}
		t := mtype(headbuf[0])
		port := binary.LittleEndian.Uint32(headbuf[1:])

		if t == mEnter {
			handleEnter(port, mc, sc)
		} else if t == mExit {
			handleExit(port, mc, sc)
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
	// should break? then should also drain mc and sc, maybe

	for {
		select {
		case m, ok := <-mc:
			if !ok {
				break
			}

			head := [5]byte{byte(mRecv)}
			binary.LittleEndian.PutUint32(head[1:], m.p)

			if _, err := w.Write(head[:]); err != nil {
				log.Printf("cli writer message head: %v", err)
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

func handleEnter(port uint32, mc chan<- mes, sc chan<- sig) {
	log.Printf("enter %d", port)
	sc <- sOkEnter
	_ = mc
}

func handleExit(port uint32, mc chan<- mes, sc chan<- sig) {
	log.Printf("exit %d", port)
	sc <- sOkExit
	_ = mc
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
	log.Printf("send %q to %d", string(bs), port)
	sc <- sOkSend
}

func handleInvalidType(sc chan<- sig) {
	log.Printf("invalid")
	sc <- sErrType
}

func main() {
	serve()
}
