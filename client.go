package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
)

type mtype byte // message type

const (
	// types of outgoing messages
	mExit  = mtype(0b000)
	mEnter = mtype(0b001)
	mSend  = mtype(0b011)

	// types of incoming messages
	mRecv = mtype(0b010)
	mSig  = mtype(0xFF)
)

type sig byte

const (
	sOkEnter = sig(0x01)
	sOkExit  = sig(0x02)
	sOkSend  = sig(0x03)
	sErrType = sig(0x80)
)

type mes struct {
	p uint32
	b []byte
}

type tmes struct {
	t mtype // type
	mes
}

func client() {
	conn, err := net.Dial("tcp", "localhost:1703")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	outgoing := make(chan tmes)

	mchan := make(chan mes) // incoming recv
	schan := make(chan sig) // incoming sig

	defer close(outgoing)

	go servWriter(conn, outgoing)     // transforms messages from outgoing into writes to conn
	go servReader(conn, mchan, schan) // reads from conn and sends to mchan and schan

	// consume mchan, schan
	go func() {
		for {
			select {
			case m, ok := <-mchan:
				if !ok {
					break
				}
				fmt.Printf("< %d ? %s\n", m.p, m.b)
			case s, ok := <-schan:
				if !ok {
					break
				}
				fmt.Printf("< SIG %x\n", s)
			}
		}
	}()

	// TODO we keep scanning even after the connection has closed

	// produce outgoing
	s := bufio.NewScanner(os.Stdin)
loop:
	for s.Scan() {
		cmd := s.Bytes()
		if len(cmd) == 0 {
			fmt.Println("invalid command")
			continue
		}
		if bytes.HasSuffix(cmd, []byte{'X', 'X', 'X'}) {
			fmt.Println("command cancelled")
			continue
		}

		var port uint32
		var body []byte
		switch {
		case send(cmd, &port, &body):
			outgoing <- tmes{mSend, mes{port, body}}
		case enter(cmd, &port):
			outgoing <- tmes{mEnter, mes{port, []byte{}}}
		case exit(cmd, &port):
			outgoing <- tmes{mExit, mes{port, []byte{}}}
		case quit(cmd):
			fmt.Println("quitting. goodbye!")
			break loop
		default:
			fmt.Println("invalid command")
		}
	}
	if err := s.Err(); err != nil {
		log.Fatal(err)
	}
}

func main() {
	client()
}

func send(cmd []byte, portp *uint32, messagep *[]byte) bool {
	portb, cmd, ok := bytes.Cut(cmd, []byte{' '})
	if !ok {
		return false
	}
	bang, cmd, ok := bytes.Cut(cmd, []byte{' '})
	if !ok {
		return false
	}
	if bang[0] != '!' {
		return false
	}
	port, err := strconv.ParseUint(string(portb), 10, 32)
	if err != nil {
		return false
	}
	*portp = uint32(port)
	*messagep = cmd
	return true
}

func enter(cmd []byte, portp *uint32) bool {
	bang, portb, ok := bytes.Cut(cmd, []byte{' '})
	if !ok {
		return false
	}
	if bang[0] != '!' {
		return false
	}
	port, err := strconv.ParseUint(string(portb), 10, 32)
	if err != nil {
		return false
	}
	*portp = uint32(port)
	return true
}

func exit(cmd []byte, portp *uint32) bool {
	dot, portb, ok := bytes.Cut(cmd, []byte{' '})
	if !ok {
		return false
	}
	if dot[0] != '.' {
		return false
	}
	port, err := strconv.ParseUint(string(portb), 10, 32)
	if err != nil {
		return false
	}
	*portp = uint32(port)
	return true
}

func quit(cmd []byte) bool {
	return cmd[0] == 'q' && len(cmd) == 1
}

func servWriter(w io.Writer, outgoing <-chan tmes) {
	for m := range outgoing {
		var head [5]byte
		head[0] = byte(m.t)
		binary.LittleEndian.PutUint32(head[1:], m.p)
		if _, err := w.Write(head[:]); err != nil {
			log.Println(err)
			break
		}

		if m.t == mSend {
			var siz [2]byte
			binary.LittleEndian.PutUint16(siz[:], uint16(len(m.b)))
			if _, err := w.Write(siz[:]); err != nil {
				log.Println(err)
				break
			}
			if _, err := w.Write(m.b); err != nil {
				log.Println(err)
				break
			}
		}
	}

	// drain
	for range outgoing {
	}
}

func servReader(r io.Reader, mc chan<- mes, sc chan<- sig) {
	defer close(mc)
	defer close(sc)

	for {
		var buf [1]byte
		if err := readFull(r, buf[:]); err != nil {
			if err != io.EOF {
				log.Printf("read t: %v", err)
			}
			break
		}
		t := mtype(buf[0])

		if t == mSig {
			if err := readFull(r, buf[:]); err != nil {
				if err != io.EOF {
					log.Printf("read sig: %v", err)
				}
				break
			}
			sc <- sig(buf[0])
		} else {
			var portbuf [4]byte
			if err := readFull(r, portbuf[:]); err != nil {
				if err != io.EOF {
					log.Printf("read port: %v", err)
				}
				break
			}
			port := binary.LittleEndian.Uint32(portbuf[:])

			var sizbuf [2]byte
			if err := readFull(r, sizbuf[:]); err != nil {
				if err != io.EOF {
					log.Printf("read size: %v", err)
				}
				break
			}
			siz := binary.LittleEndian.Uint16(sizbuf[:])

			bb := new(bytes.Buffer)
			io.Copy(bb, io.LimitReader(r, int64(siz)))
			body := bb.Bytes()

			mc <- mes{port, body}
		}
	}
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
