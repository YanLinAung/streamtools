package library

import (
	"github.com/nytlabs/streamtools/st/blocks" // blocks
)

// specify those channels we're going to use to communicate with streamtools
type Skeleton struct {
	blocks.Block
	queryrule chan chan interface{}
	inrule    chan interface{}
	inpoll    chan interface{}
	in        chan interface{}
	out       chan interface{}
	quit      chan interface{}
}

// we need to build a simple factory so that streamtools can make new blocks of this kind
func NewSkeleton() blocks.BlockInterface {
	return &Skeleton{}
}

// Setup is called once before running the block. We build up the channels and specify what kind of block this is.
func (b *Skeleton) Setup() {
	b.Kind = "Skeleton"
	b.in = b.InRoute("in")
	b.inrule = b.InRoute("rule")
	b.queryrule = b.QueryRoute("rule")
	b.inpoll = b.InRoute("poll")
	b.quit = b.InRoute("quit")
	b.out = b.Broadcast()
}

// Run is the block's main loop. Here we listen on the different channels we set up.
func (b *Skeleton) Run() {
	for {
		select {
		case <-b.inrule:
			// set a parameter of the block
		case <-b.quit:
			// quit the block
			return
		case <-b.in:
			// deal with inbound data
		case <-b.inpoll:
			// deal with a poll request
		case <-b.queryrule:
			// deal with a query request
		}
	}
}
