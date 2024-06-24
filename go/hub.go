package main

import (
	"log"
	"runtime"
	"sync"
)

type zero struct{}

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

// TODO: outgoing message and signal should include server timestamp (?)

// It's only with the hub that you should interact directly outside of this
// file. Never with a port or portlist.

type mes struct {
	p uint32 // port
	b []byte // body
}

type sig byte

const (
	sOkEnter = sig(0x01)
	sOkExit  = sig(0x02)
	sOkSend  = sig(0x03)
	sErrType = sig(0x80)
	sErrSend = sig(0x83)
)

type cli struct {
	mc   chan<- mes
	done <-chan zero
}

type port map[cli]zero

type portlist struct {
	mu    *sync.Mutex
	ports map[uint32]port
}

type hub []portlist

var thehub = newHub()

func (c cli) send(m mes) {
	select {
	case c.mc <- m:
		log.Printf("INFO (cli) client %v broadcasted %v", c.mc, m)
	case <-c.done:
		log.Printf("INFO (cli) client %v closed before could broadcast %v", c.mc, m)
	}
}

func (p port) enter(c cli) {
	p[c] = zero{}
}

func (p port) exit(c cli) {
	delete(p, c)
}

func (p port) empty() bool {
	return len(p) == 0
}

func (p port) broadcast(m mes) {
	log.Printf("INFO (port) port %d broadcasting %v", p, m)
	// Como fazer o broadcast? Algumas alternativas.
	//
	// 1. Envio síncrono
	// for c := range p { c.send(m) }
	// Problema: uma conexão lenta vai bloquear a portlist inteira.
	//
	// 2. Envio assíncrono
	// for c := range p { go c.send(m) }
	// Resolve problema do (1) e do (3) com somente 3 caracteres.
	// Possível problema: tudo bem que goroutines são leves, mas será
	// que criar uma goroutine só pra fazer um send num chan não é demais?
	//
	// 3. Envio síncrono a um buffered channel
	// Resolve possível problema do (2)
	// Problema: o tamanho de um buffered channel é fixo.
	// para alguns clientes pode ser muito (estão em poucos ports
	// e/ou ports com pouco movimento), enquanto para alguns clientes
	// pode ser pouco (estão em muitos ports e/ou ports com muito movimento).
	//
	// 4. Envio síncrono a uma "mailbox"
	// Em vez da goroutine cliente ser a mesma que escreve no seu socket,
	// ela usa uma fila de tamanho dinâmico como buffer. No seu loop, ela
	// fica num select com um case pra receber mensagem do servidor e outro
	// case pra enviar a outra goroutine que escreve no socket efetivamente.
	// Acho que seria algo assim, pelo menos. Parece o mais elegante.
	// Só teria que cuidar pra não ficar busy waiting quando não houver
	// mensagens.
	// Resolve o problema do (3).
	//
	// A questão que fica é: entre (2) e (4), qual a melhor?
	//
	// As duas tecnicamente implementam a mesma coisa: uma fila dinâmica.
	// A diferença é que a (2) usa o próprio runtime pra fazer isso.
	// Em vez de criar uma fila explícita, ela usa a própria fila de
	// goroutines do runtime.
	//
	// (É quase que nem criar uma pilha vs. fazer uma chamada recursiva...
	// interessante a analogia...)
	//
	// Para os propósitos do TCC, talvez (2) seja mais interessante, porque
	// coloca a responsabilidade no runtime.
	//
	// Aliás, o que foi discutido nesse comentário é 100% relevante pra
	// seção de desenvolvimento do TCC.

	// TODO: what if the client channel closed already?
	// this will result in a goroutine leak
	// how to avoid it?
	// probably instad of c<-m
	// we need a select that has also a "done" channel
	// so when the done channel closes the goroutine stops
	// which by its turn would require close to be properly sinchronized wrt the current code..

	for c := range p {
		go c.send(m)
	}
}

func makePortlist() portlist {
	return portlist{
		mu:    new(sync.Mutex),
		ports: make(map[uint32]port),
	}
}

func (pl portlist) enter(portid uint32, c cli) {
	log.Printf("INFO (portlist) client %v entering %v", c.mc, portid)
	pl.mu.Lock()
	defer pl.mu.Unlock()
	p, ok := pl.ports[portid]
	if !ok {
		log.Printf("INFO (portlist) creating port %v, was empty", portid)
		p = make(port)
		pl.ports[portid] = p
	}
	p.enter(c)
}

func (pl portlist) exit(portid uint32, c cli) {
	log.Printf("INFO (portlist) client %v exiting %v", c.mc, portid)
	pl.mu.Lock()
	defer pl.mu.Unlock()

	p, ok := pl.ports[portid]
	if ok {
		p.exit(c)
		if p.empty() {
			log.Printf("INFO (portlist) port empty, destroying %v", portid)
			delete(pl.ports, portid)
		}
	}
}

func (pl portlist) broadcast(portid uint32, m mes) bool {
	log.Printf("INFO (portlist) broadcasting to %v", portid)
	pl.mu.Lock()
	defer pl.mu.Unlock()

	p, ok := pl.ports[portid]
	if !ok {
		return false
	}
	p.broadcast(m)
	return true
}

func newHub() hub {
	h := make(hub, runtime.NumCPU())
	for i := range h {
		h[i] = makePortlist()
	}
	return h
}

func (h hub) portlist(portid uint32) portlist {
	i := int(portid % uint32(len(h)))
	return h[i]
}

func (h hub) enter(portid uint32, c cli) {
	h.portlist(portid).enter(portid, c)
}

func (h hub) exit(portid uint32, c cli) {
	h.portlist(portid).exit(portid, c)
}

func (h hub) broadcast(portid uint32, m mes) bool {
	return h.portlist(portid).broadcast(portid, m)
}
