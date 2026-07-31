package routingref

import "strconv"

// RouteRef identifies one physical channel route across logical protocol kinds.
type RouteRef struct {
	Kind       string `json:"kind,omitempty"`
	Index      int    `json:"index"`
	ChannelUID string `json:"channelUid,omitempty"`
}

// Key is the stable, comparable identity used by maps and sets.
// ChannelUID takes precedence; legacy routes fall back to logical kind + index.
type Key struct {
	ChannelUID string
	Kind       string
	Index      int
}

func (r RouteRef) Key() Key {
	if r.ChannelUID != "" {
		return Key{ChannelUID: r.ChannelUID}
	}
	return Key{Kind: r.Kind, Index: r.Index}
}

func (r RouteRef) IsZero() bool {
	return r.Kind == "" && r.Index == 0 && r.ChannelUID == ""
}

// Matches compares route identity while allowing legacy UID-less records to
// resolve through their logical kind and index.
func (r RouteRef) Matches(other RouteRef) bool {
	if r.ChannelUID != "" && other.ChannelUID != "" {
		return r.ChannelUID == other.ChannelUID
	}
	return r.Kind == other.Kind && r.Index == other.Index
}

func (k Key) String() string {
	if k.ChannelUID != "" {
		return "uid:" + k.ChannelUID
	}
	return "route:" + k.Kind + ":" + strconv.Itoa(k.Index)
}
