module github.com/mdlayher/zedhook

go 1.18

require (
	github.com/google/go-cmp v0.5.7
	github.com/peterbourgon/unixtransport v0.0.1
	golang.org/x/exp v0.0.0-20220328175248-053ad81199eb
)

require (
	github.com/mdlayher/netx v0.0.0-20200512211805-669a06fde734
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	inet.af/peercred v0.0.0-20210906144145-0893ea02156a
)

require golang.org/x/sys v0.0.0-20220403020550-483a9cbc67c0 // indirect

// Pending PR: https://github.com/peterbourgon/unixtransport/pull/3
replace github.com/peterbourgon/unixtransport v0.0.1 => github.com/mdlayher/unixtransport v0.0.2-0.20220403125358-b2388bd7d2a2
