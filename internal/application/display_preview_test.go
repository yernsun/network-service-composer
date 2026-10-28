package application

import (
	"strings"
	"testing"
)

func TestPreviewResolvedYAMLShowsOrdinaryVariablesAndMasksCredentials(t *testing.T) {
	if MaskCredential("1234567") != "**" || MaskCredential("12345678") != "12**78" || MaskCredential("") != "" {
		t.Fatal("credential length boundary changed")
	}
	if !IsCredentialKey("database_password") || !IsCredentialKey("API_KEY") || IsCredentialKey("REGION") {
		t.Fatal("credential key detection changed")
	}
	source := "# hidden LongSecret123\nservices:\n  api:\n    image: nginx:${TAG}\n    environment:\n      PRIVATE: ${PRIVATE}\n      API_KEY: ${API_KEY}\n      SHORT: ${SHORT}\n      VISIBLE: ${VISIBLE}\n      PASSWORD: raw**danger\n      URL: postgres://user:LongSecret123@db/app\n"
	values := map[string]string{"TAG": "1.29", "PRIVATE": "LongSecret123", "API_KEY": "other-secret-6789", "SHORT": "tiny", "VISIBLE": "region-west"}
	preview, err := PreviewResolvedYAML(source, values, []string{"PRIVATE", "SHORT"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"nginx:1.29", "Lo**23", "ot**89", "SHORT:", "region-west", "PASSWORD: ra**er", "postgres://user:Lo**23@db/app"} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("missing %q in preview: %s", expected, preview)
		}
	}
	for _, secret := range []string{"LongSecret123", "other-secret-6789", "tiny", "raw**danger"} {
		if strings.Contains(preview, secret) {
			t.Fatalf("raw credential %q in preview: %s", secret, preview)
		}
	}
	withAnchor := "x-shared: &${PRIVATE} value\nservices:\n  api:\n    image: nginx:1\n    environment:\n      REFERENCE: *${PRIVATE}\n"
	anchorPreview, err := PreviewResolvedYAML(withAnchor, values, []string{"PRIVATE"})
	if err == nil && strings.Contains(anchorPreview, values["PRIVATE"]) {
		t.Fatalf("secret in YAML anchor or alias: %s", anchorPreview)
	}
}

func TestPreviewResolvedYAMLRedactsListEnvironmentAndLiteralURLPassword(t *testing.T) {
	const password = "LiteralPassword6789"
	source := "services:\n  api:\n    image: nginx:1\n    environment:\n      - API_KEY=${API_KEY}\n      - SHORT=${SHORT}\n      - PASSWORD=" + password + "\n      - REGION=${REGION}\n      - URL=postgres://user:" + password + "@db/app\n"
	preview, err := PreviewResolvedYAML(source, map[string]string{"API_KEY": "AnotherSecret1234", "SHORT": "tiny", "REGION": "west"}, []string{"SHORT"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"API_KEY=An**34", "SHORT=**", "PASSWORD=Li**89", "REGION=west", "URL=postgres://user:Li**89@db/app"} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("missing %q from preview: %s", expected, preview)
		}
	}
	for _, raw := range []string{"AnotherSecret1234", "tiny", password} {
		if strings.Contains(preview, raw) {
			t.Fatalf("raw credential in preview: %s", preview)
		}
	}
}
