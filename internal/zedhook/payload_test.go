// Copyright 2022 Matt Layher
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zedhook

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	statusSimple = `
  pool: test
 state: ONLINE
  scan: scrub repaired 0B in 00:00:00 with 0 errors on Tue Apr  5 16:05:49 2022
config:

        NAME                      STATE     READ WRITE CKSUM  SLOW
        test                      ONLINE       0     0     0     0
          /home/matt/tmp/zfs.img  ONLINE       0     0     0     0  (uninitialized)  (untrimmed)

errors: No known data errors
`

	// zpool list -pv primary
	testList = `
NAME                                                       SIZE          ALLOC            FREE  CKPOINT  EXPANDSZ   FRAG    CAP  DEDUP    HEALTH  ALTROOT
primary                                          36971078483968  7064050565120  29907027918848        -         -      0     19   1.00    ONLINE  -
  mirror-0                                       17987323035648  3493198557184  14494124478464        -         -      0     19      -    ONLINE
    ata-ST18000NM000J-2TV103_00000000                -      -      -        -         -      -      -      -    ONLINE
    ata-ST18000NM000J-2TV103_00000001                -      -      -        -         -      -      -      -    ONLINE
  mirror-1                                       17987323035648  3505369518080  14481953517568        -         -      0     19      -    ONLINE
    ata-ST18000NM000J-2TV103_00000002                -      -      -        -         -      -      -      -    ONLINE
    ata-ST18000NM000J-2TV103_00000003                -      -      -        -         -      -      -      -    ONLINE
special                                              -      -      -        -         -      -      -      -  -
  mirror-2                                       996432412672  65482489856  930949922816        -         -      0      6      -    ONLINE
    ata-Samsung_SSD_870_EVO_1TB_000000000000000      -      -      -        -         -      -      -      -    ONLINE
    ata-Samsung_SSD_870_EVO_1TB_000000000000001      -      -      -        -         -      -      -      -    ONLINE
`

	// TODO(mdlayher): use the above.
	_ = testList
)

func Test_makePayload(t *testing.T) {
	tests := []struct {
		name   string
		envs   []string
		status execFunc
		p      Payload
	}{
		{
			name: "empty",
		},
		{
			name: "no status",
			envs: []string{
				// Ignored.
				"xxx",
				"IFS=\n",
				"ZPOOL=/sbin/zpool",
				// Extra = sign.
				"ZEVENT_TEST=foo=bar",
				"ZEVENT_CLASS=sysevent.fs.zfs.history_event",
				"ZEVENT_POOL=tank",
			},
			p: Payload{
				version: V0,
				Variables: []Variable{
					{Key: "ZEVENT_CLASS", Value: "sysevent.fs.zfs.history_event"},
					{Key: "ZEVENT_POOL", Value: "tank"},
					{Key: "ZEVENT_TEST", Value: "foo=bar"},
				},
			},
		},
		{
			name: "status",
			envs: []string{
				// Ignored.
				"ZPOOL=/sbin/zpool",
				"ZEVENT_CLASS=sysevent.fs.zfs.scrub_finish",
				"ZEVENT_POOL=tank",
			},
			status: func(_ context.Context, zpool, pool string) ([]byte, error) {
				if diff := cmp.Diff("/sbin/zpool", zpool); diff != "" {
					t.Fatalf("unexpected zpool binary (-want +got):\n%s", diff)
				}

				if diff := cmp.Diff("tank", pool); diff != "" {
					t.Fatalf("unexpected pool name (-want +got):\n%s", diff)
				}

				return []byte(statusSimple), nil
			},
			p: Payload{
				version: V0,
				Variables: []Variable{
					{Key: "ZEVENT_CLASS", Value: "sysevent.fs.zfs.scrub_finish"},
					{Key: "ZEVENT_POOL", Value: "tank"},
				},
				Zpool: &ZpoolPayload{RawStatus: statusSimple},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := makePayload(context.Background(), tt.envs, tt.status)
			if err != nil {
				t.Fatalf("failed to get payload: %v", err)
			}

			if diff := cmp.Diff(tt.p, p, cmp.AllowUnexported(Payload{})); diff != "" {
				t.Fatalf("unexpected payload (-want +got):\n%s", diff)
			}
		})
	}
}
