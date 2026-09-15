package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type callbackOptionalDeclarer interface {
	CallbackOptionalDeclared() (bool, error)
}

func TestBPMNUserTaskCallbackOptionalDeclared(t *testing.T) {
	testCallbackOptionalDeclared(t, func(extensionElements *BPMNExtensionElements) callbackOptionalDeclarer {
		return &BPMNUserTask{ExtensionElements: extensionElements}
	})
}

func TestBPMNServiceTaskCallbackOptionalDeclared(t *testing.T) {
	testCallbackOptionalDeclared(t, func(extensionElements *BPMNExtensionElements) callbackOptionalDeclarer {
		return &BPMNServiceTask{ExtensionElements: extensionElements}
	})
}

func testCallbackOptionalDeclared(
	t *testing.T,
	newTask func(extensionElements *BPMNExtensionElements) callbackOptionalDeclarer,
) {
	t.Helper()

	tests := []struct {
		name              string
		extensionElements *BPMNExtensionElements
		want              bool
		wantError         bool
	}{
		{name: "absent extension elements", want: false},
		{name: "absent metadata", extensionElements: &BPMNExtensionElements{}, want: false},
		{
			name: "exact true",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_optional", Value: "true"},
			}},
			want: true,
		},
		{
			name: "trimmed false",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_optional", Value: " \n\tfalse\r "},
			}},
			want: false,
		},
		{
			name: "unrelated metadata does not declare optionality",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_config_ref", Value: "connector-1"},
			}},
			want: false,
		},
		{
			name: "uppercase rejected",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_optional", Value: "TRUE"},
			}},
			wantError: true,
		},
		{
			name: "mixed case rejected",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_optional", Value: "False"},
			}},
			wantError: true,
		},
		{
			name: "numeric rejected",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_optional", Value: "1"},
			}},
			wantError: true,
		},
		{
			name: "empty rejected",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_optional", Value: " \t"},
			}},
			wantError: true,
		},
		{
			name: "arbitrary text rejected",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_optional", Value: "yes"},
			}},
			wantError: true,
		},
		{
			name: "duplicate same value rejected",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_optional", Value: "true"},
				{Name: "callback_optional", Value: "true"},
			}},
			wantError: true,
		},
		{
			name: "duplicate conflicting values rejected",
			extensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
				{Name: "callback_optional", Value: "true"},
				{Name: "callback_optional", Value: "false"},
			}},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newTask(tt.extensionElements).CallbackOptionalDeclared()
			if tt.wantError {
				require.Error(t, err)
				require.False(t, got, "invalid or duplicate optionality must fail closed")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
