package tmpl_test

import (
	"errors"
	"reflect"
	"testing"

	"wongkito/api/internal/tmpl"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name       string
		template   string
		info       map[string]string
		want       string
		wantErr    error
		wantAnyErr bool
	}{
		{
			name:     "pipe separator trims values",
			template: `{player_id}|{zone_id}`,
			info:     map[string]string{"player_id": " 12345678 ", "zone_id": "1234"},
			want:     "12345678|1234",
		},
		{
			name:     "adjacent placeholders",
			template: `{player_id}{zone_id}`,
			info:     map[string]string{"player_id": " 12345678 ", "zone_id": "1234"},
			want:     "123456781234",
		},
		{
			name:     "digits filter",
			template: `{phone|digits}`,
			info:     map[string]string{"phone": "0812-3456-7890"},
			want:     "081234567890",
		},
		{
			name:     "msisdn rewrites 62 to 0",
			template: `{phone|msisdn}`,
			info:     map[string]string{"phone": "+62 812 3456 7890"},
			want:     "081234567890",
		},
		{
			name:     "msisdn leaves local number",
			template: `{phone|msisdn}`,
			info:     map[string]string{"phone": "081234567890"},
			want:     "081234567890",
		},
		{
			name:     "missing key",
			template: `{player_id}|{zone_id}`,
			info:     map[string]string{"player_id": "12345678"},
			wantErr:  tmpl.ErrMissingKey,
		},
		{
			name:     "digits filter empty",
			template: `{x|digits}`,
			info:     map[string]string{"x": "abc"},
			wantErr:  tmpl.ErrEmpty,
		},
		{
			name:       "unknown filter",
			template:   `{x|upper}`,
			info:       map[string]string{"x": "abc"},
			wantAnyErr: true,
		},
		{
			name:     "unclosed brace",
			template: `{player_id}|{zone_id`,
			info:     map[string]string{"player_id": "123", "zone_id": "1"},
			wantErr:  tmpl.ErrUnmatchedBrace,
		},
		{
			name:     "stray closing brace",
			template: `{player_id}}`,
			info:     map[string]string{"player_id": "123"},
			wantErr:  tmpl.ErrUnmatchedBrace,
		},
		{
			name:     "empty template",
			template: "  ",
			info:     nil,
			wantErr:  tmpl.ErrEmpty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tmpl.Render(tt.template, tt.info)
			if tt.wantAnyErr {
				if err == nil {
					t.Fatalf("Render() error = nil, want an error")
				}
				return
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Render() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKeys(t *testing.T) {
	got := tmpl.Keys(`{a}|{b|digits}{a}`)
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys() = %#v, want %#v", got, want)
	}
}
