package security

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/klauspost/reedsolomon"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

const (
	fecTypeRaw   = 0x00
	fecTypeShard = 0x01

	fecShardHeaderSize = 7
	fecBypassThreshold = 100
)

type FECPipe struct {
	inner        transport.Pipe
	dataShards   int
	parityShards int
	enc          reedsolomon.Encoder

	mu  sync.Mutex
	seq uint32

	recvMu     sync.Mutex
	recvGroups map[uint32]*recvGroup
}

type recvGroup struct {
	shards [][]byte
	parity [][]byte
	total  int
}

func NewFECPipe(inner transport.Pipe, dataShards, parityShards int) (*FECPipe, error) {
	if dataShards <= 0 || parityShards < 0 {
		return nil, errors.New("fec-pipe: invalid shard count")
	}
	if dataShards+parityShards > 256 {
		return nil, errors.New("fec-pipe: too many shards (max 256)")
	}
	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, err
	}
	return &FECPipe{
		inner:        inner,
		dataShards:   dataShards,
		parityShards: parityShards,
		enc:          enc,
		recvGroups:   make(map[uint32]*recvGroup),
	}, nil
}

func (p *FECPipe) Start(ctx context.Context) error { return p.inner.Start(ctx) }
func (p *FECPipe) Stop(ctx context.Context) error  { return p.inner.Stop(ctx) }
func (p *FECPipe) SessionID() string               { return p.inner.SessionID() }
func (p *FECPipe) MaxMessageSize() int             { return p.inner.MaxMessageSize() }

func (p *FECPipe) SendMessage(ctx context.Context, data []byte, key string) error {
	if len(data) == 0 {
		return p.inner.SendMessage(ctx, data, key)
	}

	if len(data) < fecBypassThreshold {
		buf := make([]byte, 1+len(data))
		buf[0] = fecTypeRaw
		copy(buf[1:], data)
		return p.inner.SendMessage(ctx, buf, key)
	}

	shards, err := p.enc.Split(data)
	if err != nil {
		return err
	}
	if err := p.enc.Encode(shards); err != nil {
		return err
	}

	p.mu.Lock()
	seq := p.seq
	p.seq++
	p.mu.Unlock()

	total := len(shards)

	for i, shard := range shards {
		buf := make([]byte, fecShardHeaderSize+len(shard))
		buf[0] = fecTypeShard
		binary.BigEndian.PutUint32(buf[1:5], seq)
		buf[5] = byte(i)
		buf[6] = byte(total)
		copy(buf[fecShardHeaderSize:], shard)

		shardKey := fmt.Sprintf("fec:%d:%d", seq, i)
		if err := p.inner.SendMessage(ctx, buf, shardKey); err != nil {
			return err
		}
	}
	return nil
}

func (p *FECPipe) ReceiveMessage(ctx context.Context) ([]byte, error) {
	for {
		buf, err := p.inner.ReceiveMessage(ctx)
		if err != nil {
			return nil, err
		}
		if len(buf) < 1 {
			continue
		}

		switch buf[0] {
		case fecTypeRaw:
			out := make([]byte, len(buf)-1)
			copy(out, buf[1:])
			return out, nil

		case fecTypeShard:
			if len(buf) < fecShardHeaderSize {
				continue
			}
			seq := binary.BigEndian.Uint32(buf[1:5])
			idx := int(buf[5])
			total := int(buf[6])
			payload := buf[fecShardHeaderSize:]

			if total != p.dataShards+p.parityShards {
				continue
			}
			if idx < 0 || idx >= total {
				continue
			}

			p.recvMu.Lock()
			grp, ok := p.recvGroups[seq]
			if !ok {
				grp = &recvGroup{
					shards: make([][]byte, p.dataShards),
					parity: make([][]byte, p.parityShards),
					total:  total,
				}
				p.recvGroups[seq] = grp
			}

			if idx < p.dataShards {
				grp.shards[idx] = payload
			} else {
				grp.parity[idx-p.dataShards] = payload
			}

			allData := true
			for _, s := range grp.shards {
				if s == nil {
					allData = false
					break
				}
			}
			if allData {
				var out []byte
				for _, s := range grp.shards {
					out = append(out, s...)
				}
				delete(p.recvGroups, seq)
				p.recvMu.Unlock()
				return out, nil
			}
			p.recvMu.Unlock()

		default:
			continue
		}
	}
}

func (p *FECPipe) DeleteMessage(ctx context.Context, key string) error {
	return p.inner.DeleteMessage(ctx, key)
}