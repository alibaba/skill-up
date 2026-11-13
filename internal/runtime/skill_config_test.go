package runtime

import "testing"

func TestSkillConfigDirName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  SkillConfig
		want string
	}{
		{
			name: "declared name wins over source basename",
			cfg:  SkillConfig{Source: "/tmp/snap", Name: "my-skill"},
			want: "my-skill",
		},
		{
			name: "empty name falls back to source basename",
			cfg:  SkillConfig{Source: "/tmp/snap"},
			want: "snap",
		},
		{
			name: "whitespace-only name falls back to source basename",
			cfg:  SkillConfig{Source: "/tmp/snap", Name: "   "},
			want: "snap",
		},
		{
			name: "current directory name falls back to source basename",
			cfg:  SkillConfig{Source: "/tmp/snap", Name: "."},
			want: "snap",
		},
		{
			name: "parent directory name falls back to source basename",
			cfg:  SkillConfig{Source: "/tmp/snap", Name: ".."},
			want: "snap",
		},
		{
			name: "name with slash falls back to source basename",
			cfg:  SkillConfig{Source: "/tmp/snap", Name: "a/b"},
			want: "snap",
		},
		{
			name: "name with backslash falls back to source basename",
			cfg:  SkillConfig{Source: "/tmp/snap", Name: `a\b`},
			want: "snap",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.cfg.DirName(); got != tt.want {
				t.Fatalf("DirName() = %q, want %q", got, tt.want)
			}
		})
	}
}
