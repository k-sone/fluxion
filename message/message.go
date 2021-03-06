package message

import (
	"io"
	"reflect"

	"github.com/ugorji/go/codec"
	"github.com/yosisa/fluxion/buffer"
)

type Encoder interface {
	Encode(interface{}) error
}

type Decoder interface {
	Decode(interface{}) error
}

type MessageType uint8

const (
	TypInfoRequest MessageType = iota
	TypInfoResponse
	TypBufferOption
	TypConfigure
	TypStart
	TypStop
	TypTerminated
	TypEvent
	TypEventChain
	TypStdout
)

type Message struct {
	Type    MessageType
	UnitID  int32
	Payload interface{}
}

func (m *Message) Encode(enc Encoder) (err error) {
	if err = enc.Encode(m.UnitID); err != nil {
		return
	}
	return enc.Encode(m.Payload)
}

func (m *Message) Decode(dec Decoder) (err error) {
	if err = dec.Decode(&m.UnitID); err != nil {
		return
	}

	switch m.Type {
	case TypInfoResponse:
		var info PluginInfo
		err = dec.Decode(&info)
		m.Payload = &info
	case TypBufferOption:
		var opts buffer.Options
		err = dec.Decode(&opts)
		m.Payload = &opts
	case TypConfigure:
		var s string
		err = dec.Decode(&s)
		m.Payload = s
	case TypEvent, TypEventChain:
		var ev Event
		err = dec.Decode(&ev)
		m.Payload = &ev
	default:
		err = dec.Decode(&m.Payload)
	}
	return
}

type PluginInfo struct {
	ProtoVer uint8 `codec:"proto_ver"`
}

var mh = &codec.MsgpackHandle{RawToString: true, WriteExt: true}

func NewEncoder(w io.Writer) Encoder {
	return codec.NewEncoder(w, mh)
}

func NewDecoder(r io.Reader) Decoder {
	return codec.NewDecoder(r, mh)
}

func init() {
	mh.MapType = reflect.TypeOf(map[string]interface{}(nil))
}
