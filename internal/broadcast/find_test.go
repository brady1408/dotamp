package broadcast

import (
	"testing"

	"github.com/disgoorg/snowflake/v2"
)

type stateList [][3]snowflake.ID

func (s stateList) Each(fn func(guild, channel, user snowflake.ID) bool) {
	for _, st := range s {
		if !fn(st[0], st[1], st[2]) {
			return
		}
	}
}

func TestFindLocatesTheUser(t *testing.T) {
	vs := stateList{{1, 10, 100}, {2, 20, 200}, {3, 30, 200}}
	if loc, ok := Find(vs, 200); !ok || loc.Guild != 2 || loc.Channel != 20 {
		t.Fatalf("first match wins: %+v %v", loc, ok)
	}
	if _, ok := Find(vs, 999); ok {
		t.Fatal("absent user")
	}
	if _, ok := Find(stateList{{1, 0, 100}}, 100); ok {
		t.Fatal("a voice state with no channel means not in voice")
	}
}
