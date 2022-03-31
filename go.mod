module github.com/mdlayher/zedhook

go 1.18

require (
	github.com/google/go-cmp v0.5.7
	github.com/peterbourgon/unixtransport v0.0.1
	golang.org/x/exp v0.0.0-20220328175248-053ad81199eb
)

// Pending PR: https://github.com/peterbourgon/unixtransport/pull/3
replace github.com/peterbourgon/unixtransport v0.0.1 => github.com/mdlayher/unixtransport v0.0.2-0.20220330164218-1bd0a65e57cf
