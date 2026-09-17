package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Check only the two new reserved declarations before encoding/xml can discard
// unsupported locations. Existing descriptive/vendor metadata is unaffected.
func validateLifecycleMetadataLocations(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var path []string
	reservedDepth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch element := token.(type) {
		case xml.StartElement:
			if reservedDepth > 0 {
				return fmt.Errorf("lifecycle metadata must contain text only")
			}
			path = append(path, element.Name.Local)
			key := ""
			for _, attr := range element.Attr {
				if attr.Name.Local == lifecycleContractMetadata || attr.Name.Local == lifecyclePrerequisiteMetadata {
					return fmt.Errorf("%s must use extensionElements metaData", attr.Name.Local)
				}
				if element.Name.Local == "metaData" && attr.Name.Local == "name" {
					key = attr.Value
				}
			}
			expected := ""
			switch key {
			case lifecycleContractMetadata:
				expected = "definitions/process/extensionElements/metaData"
			case lifecyclePrerequisiteMetadata:
				expected = "definitions/process/userTask/extensionElements/metaData"
			default:
				continue
			}
			if strings.Join(path, "/") != expected {
				return fmt.Errorf("%s has unsupported XML location", key)
			}
			reservedDepth = len(path)
		case xml.EndElement:
			if len(path) == reservedDepth {
				reservedDepth = 0
			}
			path = path[:len(path)-1]
		}
	}
}
