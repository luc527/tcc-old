package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"log"
	"net"
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

	mc := make(chan mes)    // channel for outgoing messages
	sc := make(chan sig)    // channel for outgoing signals
	done := make(chan zero) // channel for signaling when the client disconnected

	go cliWriter(conn, mc, sc)    // read from the channels and write to the conn
	cliReader(conn, mc, sc, done) // read from the conn and write to the channels (now or later when a message from another user is broadcasted)
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

func readport(r io.Reader, s []byte) (uint32, bool) {
	// len(dest) should be 4
	if err := readFull(r, s); err != nil {
		if err != io.EOF {
			log.Printf("ERROR (server) client read head: %v", err)
		} else {
			log.Printf("INFO (server) client disconnected")
		}
		return 0, false
	}
	return binary.LittleEndian.Uint32(s), true
}

func cliReader(r io.Reader, mc chan<- mes, sc chan<- sig, done chan zero) {
	defer close(done)
	defer close(mc)
	defer close(sc)

	for {
		var headbuf [5]byte

		tslice := headbuf[:1]
		portslice := headbuf[1:]

		if err := readFull(r, tslice); err != nil {
			if err != io.EOF {
				log.Printf("ERROR (server) client read head: %v", err)
			} else {
				log.Printf("INFO (server) client %v disconnected", mc)
			}
			break
		}
		t := mtype(headbuf[0])

		if t == mEnter {
			p, ok := readport(r, portslice)
			if !ok {
				break
			}
			log.Printf("INFO (server) client %v entering %d", mc, p)
			thehub.enter(p, cli{mc, done})
			sc <- sOkEnter
		} else if t == mExit {
			p, ok := readport(r, portslice)
			if !ok {
				break
			}
			log.Printf("INFO (server) client %v exiting %d", mc, p)
			thehub.exit(p, cli{mc, done})
			sc <- sOkExit
		} else if t == mSend {
			p, ok := readport(r, portslice)
			if !ok {
				break
			}
			log.Printf("INFO (server) client %v sending message to %d", mc, p)
			bs, err := readBody(r)
			if err != nil {
				if err != io.EOF {
					log.Printf("ERROR (server) client read message: %v", err)
				}
				return
			}
			if sent := thehub.broadcast(p, mes{p, bs}); sent {
				sc <- sOkSend
			} else {
				sc <- sErrSend
			}
		} else {
			log.Printf("INFO (server) client %v entered invalid message", mc)
			sc <- sErrType
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
			log.Printf("INFO (server) writing message to cli %v", mc)

			head := [7]byte{byte(mRecv)}
			binary.LittleEndian.PutUint32(head[1:5], m.p)
			binary.LittleEndian.PutUint16(head[5:7], uint16(len(m.b)))

			if _, err := w.Write(head[:]); err != nil {
				log.Printf("ERROR (server) cli writer message head: %v", err)
			} else if _, err := w.Write(m.b); err != nil {
				log.Printf("ERROR (server) cli writer message body: %v", err)
			}
		case s, ok := <-sc:
			if !ok {
				break
			}
			log.Printf("INFO (server) writing signal %02x to cli %v", s, mc)
			sig := [2]byte{
				byte(mSig),
				byte(s),
			}
			if _, err := w.Write(sig[:]); err != nil {
				log.Printf("ERROR (server) cli writer signal %v", err)
			}
		}
	}
}

func readBody(r io.Reader) ([]byte, error) {
	var sizbuf [2]byte
	if err := readFull(r, sizbuf[:]); err != nil {
		return nil, err
	}
	siz := binary.LittleEndian.Uint16(sizbuf[:])

	// this is nice
	b := new(bytes.Buffer)
	if _, err := io.Copy(b, io.LimitReader(r, int64(siz))); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
