package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// validateLifecycleMetadataLocations runs before encoding/xml can silently drop
// unsupported locations, so it is the only thing standing between a misplaced
// reserved declaration and a process that publishes as if it had no contract.
// These cases call it directly: the parser-level tests in
// bpmn_workitem_lifecycle_access_test.go reach it through ParseXML and stop at
// the first error, which leaves the text-only branch unexercised.
func TestValidateLifecycleMetadataLocations(t *testing.T) {
	contract := lifecycleMetadata(lifecycleContractMetadata, "generic_fulfillment_v1")
	prereq := lifecycleMetadata(lifecyclePrerequisiteMetadata, "assigned")

	for _, tc := range []struct {
		name    string
		xml     string
		wantErr string
	}{
		{
			name: "contract at process extensionElements",
			xml:  `<definitions><process id="p"><extensionElements>` + contract + `</extensionElements></process></definitions>`,
		},
		{
			name: "prerequisite at userTask extensionElements",
			xml:  `<definitions><process id="p"><userTask id="w"><extensionElements>` + prereq + `</extensionElements></userTask></process></definitions>`,
		},
		{
			name: "both at their declared locations",
			xml:  string(lifecycleXML(contract, prereq, lifecycleOwnerAttrs, "")),
		},
		{
			name: "no reserved metadata",
			xml:  `<definitions><process id="p"><extensionElements>` + lifecycleMetadata("service_task_type", "ticket_task") + `</extensionElements></process></definitions>`,
		},
		{
			name: "unrelated metadata may nest",
			xml:  `<definitions><process id="p"><extensionElements><metaData name="service_task_type"><nested/></metaData></extensionElements></process></definitions>`,
		},
		{
			name: "empty document",
			xml:  ``,
		},
		{
			name:    "contract as attribute",
			xml:     `<definitions><process id="p" ` + lifecycleContractMetadata + `="generic_fulfillment_v1"/></definitions>`,
			wantErr: lifecycleContractMetadata + " must use extensionElements metaData",
		},
		{
			name:    "prerequisite as attribute",
			xml:     `<definitions><process id="p"><userTask id="w" ` + lifecyclePrerequisiteMetadata + `="assigned"/></process></definitions>`,
			wantErr: lifecyclePrerequisiteMetadata + " must use extensionElements metaData",
		},
		{
			name:    "contract on userTask",
			xml:     `<definitions><process id="p"><userTask id="w"><extensionElements>` + contract + `</extensionElements></userTask></process></definitions>`,
			wantErr: lifecycleContractMetadata + " has unsupported XML location",
		},
		{
			name:    "prerequisite at process level",
			xml:     `<definitions><process id="p"><extensionElements>` + prereq + `</extensionElements></process></definitions>`,
			wantErr: lifecyclePrerequisiteMetadata + " has unsupported XML location",
		},
		{
			name:    "contract nested under subProcess",
			xml:     `<definitions><process id="p"><subProcess id="s"><extensionElements>` + contract + `</extensionElements></subProcess></process></definitions>`,
			wantErr: lifecycleContractMetadata + " has unsupported XML location",
		},
		{
			name:    "contract carries child elements",
			xml:     `<definitions><process id="p"><extensionElements><metaData name="` + lifecycleContractMetadata + `">generic_fulfillment_v1<child/></metaData></extensionElements></process></definitions>`,
			wantErr: "lifecycle metadata must contain text only",
		},
		{
			name:    "prerequisite carries child elements",
			xml:     `<definitions><process id="p"><userTask id="w"><extensionElements><metaData name="` + lifecyclePrerequisiteMetadata + `">assigned<child/></metaData></extensionElements></userTask></process></definitions>`,
			wantErr: "lifecycle metadata must contain text only",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLifecycleMetadataLocations([]byte(tc.xml))
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestValidateLifecycleMetadataLocationsRejectsMalformedXML(t *testing.T) {
	err := validateLifecycleMetadataLocations([]byte(`<definitions><process id="p">`))
	require.Error(t, err)
}

// A description that merely mentions a reserved name is not a declaration, and
// must keep working: the check keys on metaData/@name and attributes only.
func TestValidateLifecycleMetadataLocationsIgnoresDescriptiveText(t *testing.T) {
	xml := `<definitions><process id="p"><extensionElements><metaData name="description">uses ` +
		lifecycleContractMetadata + ` in prose</metaData></extensionElements></process></definitions>`
	require.NoError(t, validateLifecycleMetadataLocations([]byte(xml)))

	xml = strings.Replace(xml, `<metaData name="description">uses `+lifecycleContractMetadata+` in prose</metaData>`, `<metaData name="description"/><documentation>see `+lifecyclePrerequisiteMetadata+`</documentation>`, 1)
	require.NoError(t, validateLifecycleMetadataLocations([]byte(xml)))
}

// The decoder walks the whole document, so a reserved declaration is caught even
// when it appears after unrelated content that the parser would otherwise reach
// first.
func TestValidateLifecycleMetadataLocationsScansWholeDocument(t *testing.T) {
	xml := `<definitions><process id="p"><extensionElements><metaData name="service_task_type">ticket_task</metaData>` +
		`<metaData name="` + lifecycleContractMetadata + `">generic_fulfillment_v1</metaData></extensionElements>` +
		`<userTask id="w"><extensionElements><metaData name="` + lifecyclePrerequisiteMetadata + `">assigned<child/></metaData></extensionElements></userTask></process></definitions>`
	require.ErrorContains(t, validateLifecycleMetadataLocations([]byte(xml)), "lifecycle metadata must contain text only")
}
