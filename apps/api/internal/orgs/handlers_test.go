package orgs

import "testing"

func TestSettingsBoundary(t *testing.T) {
	good := settingsInput{Name: "School", Locale: "en-IN", Timezone: "Asia/Kolkata"}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []settingsInput{{Name: "", Locale: "en", Timezone: "UTC"}, {Name: "School", Locale: "<script>", Timezone: "UTC"}, {Name: "School", Locale: "en", Timezone: "not-a-zone"}} {
		if bad.validate() == nil {
			t.Fatal("accepted invalid settings")
		}
	}
}
