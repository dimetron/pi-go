package tools

import "testing"

func TestStripRuntimeNoise(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty stays empty",
			input: "",
			want:  "",
		},
		{
			name:  "ordinary stderr is untouched",
			input: "go: cannot find main module\nexit status 1\n",
			want:  "go: cannot find main module\nexit status 1\n",
		},
		{
			name:  "MallocStackLogging with name(pid) prefix",
			input: "msl(16169) MallocStackLogging: can't turn off malloc stack logging because it was not enabled.\n",
			want:  "",
		},
		{
			name:  "MallocStackLogging without prefix",
			input: "MallocStackLogging: can't turn off malloc stack logging because it was not enabled.\n",
			want:  "",
		},
		{
			name:  "nano zone chatter",
			input: "node(4821) malloc: nano zone abandoned due to inability to preallocate reserved vm space.\n",
			want:  "",
		},
		{
			name: "every libmalloc variant we have observed",
			input: "true(15462) MallocStackLogging: could not tag MSL-related memory as no_footprint, so those pages will be included in process footprint - No such file or directory (2)\n" +
				"true(15462) MallocStackLogging: recording malloc (and VM allocation) stacks using lite mode\n" +
				"true(15462) MallocStackLogging: stack logging disabled due to previous errors.\n" +
				"true(15480) MallocStackLogging: stack logging compaction turned off; size of log files on disk can increase rapidly\n",
			want: "",
		},
		{
			name:  "real error survives alongside noise",
			input: "msl(16169) MallocStackLogging: can't turn off malloc stack logging because it was not enabled.\nfatal: not a git repository\n",
			want:  "fatal: not a git repository\n",
		},
		{
			name:  "noise interleaved mid-output keeps surrounding lines in order",
			input: "building...\nfoo(1) MallocStackLogging: recording malloc stacks\nbuild failed\n",
			want:  "building...\nbuild failed\n",
		},
		{
			name:  "the word malloc in real output is not noise",
			input: "main.c:12: warning: implicit declaration of function 'malloc'\n",
			want:  "main.c:12: warning: implicit declaration of function 'malloc'\n",
		},
		{
			name:  "a line merely mentioning MallocStackLogging is not noise",
			input: "checking whether MallocStackLogging: is set... no\n",
			want:  "checking whether MallocStackLogging: is set... no\n",
		},
		{
			name:  "output without a trailing newline is preserved",
			input: "boom(9) MallocStackLogging: nope\nreal failure",
			want:  "real failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripRuntimeNoise(tt.input); got != tt.want {
				t.Errorf("stripRuntimeNoise(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsRuntimeNoise(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"MallocStackLogging: warning", true},
		{"x(123) MallocStackLogging: warning", true},
		{"malloc: nano zone abandoned due to pressure", true},
		{"x(123) malloc: nano zone abandoned due to pressure", true},
		{"malloc allocation failed", false},
		{"ordinary stderr", false},
	}
	for _, tt := range tests {
		if got := isRuntimeNoise(tt.line); got != tt.want {
			t.Errorf("isRuntimeNoise(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}
