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

const testStatus = `
  pool: test
 state: ONLINE
  scan: scrub repaired 0B in 03:49:05 with 0 errors on Sun Apr  3 05:49:29 2022
config:

        NAME                                                                   STATE     READ WRITE CKSUM  SLOW
        primary                                                                ONLINE       0     0     0     0
          mirror-0                                                             ONLINE       0     0     0     0
            /dev/disk/by-id/ata-ST18000NM000J-2TV103_00000000-part1            ONLINE       0     0     0     0  (uninitialized)  (trim unsupported)
            /dev/disk/by-id/ata-ST18000NM000J-2TV103_00000001-part1            ONLINE       0     0     0     0  (uninitialized)  (trim unsupported)
          mirror-1                                                             ONLINE       0     0     0     0
            /dev/disk/by-id/ata-ST18000NM000J-2TV103_00000002-part1            ONLINE       0     0     0     0  (uninitialized)  (trim unsupported)
            /dev/disk/by-id/ata-ST18000NM000J-2TV103_00000003-part1            ONLINE       0     0     0     0  (uninitialized)  (trim unsupported)
        special
          mirror-2                                                             ONLINE       0     0     0     0
            /dev/disk/by-id/ata-Samsung_SSD_870_EVO_1TB_000000000000000-part1  ONLINE       0     0     0     0  (uninitialized)  (100% trimmed, completed at Mon 04 Apr 2022 12:00:46 AM EDT)
            /dev/disk/by-id/ata-Samsung_SSD_870_EVO_1TB_000000000000001-part1  ONLINE       0     0     0     0  (uninitialized)  (100% trimmed, completed at Mon 04 Apr 2022 12:00:46 AM EDT)

errors: No known data errors
`

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

				return []byte(testStatus), nil
			},
			p: Payload{
				version: V0,
				Variables: []Variable{
					{Key: "ZEVENT_CLASS", Value: "sysevent.fs.zfs.scrub_finish"},
					{Key: "ZEVENT_POOL", Value: "tank"},
				},
				Zpool: &ZpoolPayload{RawStatus: testStatus},
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
