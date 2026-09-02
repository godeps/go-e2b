//go:build integration

package e2b

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func ownedTemplateWithAlias(t *testing.T, client *Client) TemplateDetail {
	t.Helper()

	templates, err := client.ListTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	for _, tmpl := range templates {
		if len(tmpl.Aliases) > 0 {
			return tmpl
		}
	}
	t.Skip("no owned template with an alias")
	return TemplateDetail{}
}

func tagSet(tags []TemplateTag) map[string]TemplateTag {
	out := make(map[string]TemplateTag, len(tags))
	for _, tag := range tags {
		out[tag.Tag] = tag
	}
	return out
}

func TestIntegrationGetTemplateAlias(t *testing.T) {
	client := integrationClient(t)
	ctx := context.Background()
	tmpl := ownedTemplateWithAlias(t, client)
	alias := tmpl.Aliases[0]

	got, err := client.GetTemplateAlias(ctx, alias)
	if err != nil {
		t.Fatalf("GetTemplateAlias(%q): %v", alias, err)
	}
	t.Logf("alias %q -> templateID=%s public=%v", alias, got.TemplateID, got.Public)
	if got.TemplateID != tmpl.TemplateID {
		t.Errorf("TemplateID = %q, want %q", got.TemplateID, tmpl.TemplateID)
	}

	tagged, err := client.GetTemplateAlias(ctx, alias+":default")
	if err != nil {
		t.Fatalf("GetTemplateAlias(%q:default): %v", alias, err)
	}
	if tagged.TemplateID != tmpl.TemplateID {
		t.Errorf("tagged TemplateID = %q, want %q", tagged.TemplateID, tmpl.TemplateID)
	}

	byID, err := client.GetTemplateAlias(ctx, tmpl.TemplateID)
	if err != nil {
		t.Fatalf("GetTemplateAlias(id): %v", err)
	}
	if byID.TemplateID != tmpl.TemplateID {
		t.Errorf("id TemplateID = %q, want %q", byID.TemplateID, tmpl.TemplateID)
	}

	if len(tmpl.Names) > 0 {
		ns, err := client.GetTemplateAlias(ctx, tmpl.Names[0])
		if err != nil {
			t.Fatalf("GetTemplateAlias(%q): %v", tmpl.Names[0], err)
		}
		if ns.TemplateID != tmpl.TemplateID {
			t.Errorf("namespaced TemplateID = %q, want %q", ns.TemplateID, tmpl.TemplateID)
		}
	}
}

func TestIntegrationGetTemplateAliasNotFound(t *testing.T) {
	client := integrationClient(t)

	_, err := client.GetTemplateAlias(context.Background(), "this-alias-does-not-exist-xyz")
	var nfe *TemplateNotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("expected *TemplateNotFoundError, got %T: %v", err, err)
	}
}

func TestIntegrationGetTemplateAliasForbiddenBase(t *testing.T) {
	client := integrationClient(t)

	got, err := client.GetTemplateAlias(context.Background(), "base")
	if err == nil {
		t.Logf("this account can access alias base: templateID=%s public=%v", got.TemplateID, got.Public)
		return
	}
	var nfe *TemplateNotFoundError
	if errors.As(err, &nfe) {
		t.Fatal("GetTemplateAlias(\"base\") must not map 403 to *TemplateNotFoundError")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("StatusCode = %d, want 403: %v", apiErr.StatusCode, err)
	}
	t.Logf("base alias correctly returned 403: %v", err)
}

func TestIntegrationListTemplateTags(t *testing.T) {
	client := integrationClient(t)
	ctx := context.Background()
	tmpl := ownedTemplateWithAlias(t, client)

	byID, err := client.ListTemplateTags(ctx, tmpl.TemplateID)
	if err != nil {
		t.Fatalf("ListTemplateTags(id): %v", err)
	}
	if _, ok := tagSet(byID)["default"]; !ok {
		t.Fatalf("expected default tag on %s, got %+v", tmpl.TemplateID, byID)
	}

	byAlias, err := client.ListTemplateTags(ctx, tmpl.Aliases[0])
	if err != nil {
		t.Fatalf("ListTemplateTags(alias): %v", err)
	}
	if len(byAlias) != len(byID) {
		t.Errorf("alias tag count = %d, id tag count = %d", len(byAlias), len(byID))
	}

	if len(tmpl.Names) > 0 {
		byName, err := client.ListTemplateTags(ctx, tmpl.Names[0])
		if err != nil {
			t.Fatalf("ListTemplateTags(name): %v", err)
		}
		if len(byName) != len(byID) {
			t.Errorf("name tag count = %d, id tag count = %d", len(byName), len(byID))
		}
	}
}

func TestIntegrationListTemplateTagsNotFound(t *testing.T) {
	client := integrationClient(t)

	_, err := client.ListTemplateTags(context.Background(), "does-not-exist-tmpl")
	var nfe *TemplateNotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("expected *TemplateNotFoundError, got %T: %v", err, err)
	}
}

func TestIntegrationAssignAndRemoveTemplateTags(t *testing.T) {
	client := integrationClient(t)
	ctx := context.Background()
	tmpl := ownedTemplateWithAlias(t, client)
	alias := tmpl.Aliases[0]
	prefix := fmt.Sprintf("go-e2b-sdk-tags-%d", time.Now().UnixNano())
	tag1 := prefix
	tag2 := prefix + "-2"
	tag3 := prefix + "-3"
	tagNS := prefix + "-ns"
	tagBID := prefix + "-bid"
	probeTags := []string{tag1, tag2, tag3, tagNS, tagBID}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if err := client.RemoveTemplateTags(cleanupCtx, alias, probeTags...); err != nil {
			t.Logf("cleanup RemoveTemplateTags(%s): %v", alias, err)
		}
		if len(tmpl.Names) > 0 {
			if err := client.RemoveTemplateTags(cleanupCtx, tmpl.Names[0], probeTags...); err != nil {
				t.Logf("cleanup RemoveTemplateTags(%s): %v", tmpl.Names[0], err)
			}
		}
		remaining, err := client.ListTemplateTags(cleanupCtx, tmpl.TemplateID)
		if err != nil {
			t.Logf("cleanup ListTemplateTags: %v", err)
			return
		}
		for _, tag := range remaining {
			if strings.HasPrefix(tag.Tag, prefix) {
				t.Errorf("leftover probe tag %q still on %s", tag.Tag, tmpl.TemplateID)
			}
		}
	})

	assigned, err := client.AssignTemplateTags(ctx, alias+":default", tag1)
	if err != nil {
		t.Fatalf("AssignTemplateTags(single): %v", err)
	}
	t.Logf("assigned %v on build %s", assigned.Tags, assigned.BuildID)
	if assigned.BuildID == "" {
		t.Fatal("assigned BuildID is empty")
	}
	if len(assigned.Tags) != 1 || assigned.Tags[0] != tag1 {
		t.Errorf("assigned.Tags = %v, want [%s]", assigned.Tags, tag1)
	}
	buildID := assigned.BuildID

	listed, err := client.ListTemplateTags(ctx, tmpl.TemplateID)
	if err != nil {
		t.Fatalf("ListTemplateTags after assign: %v", err)
	}
	got := tagSet(listed)
	if _, ok := got["default"]; !ok {
		t.Fatal("default tag missing after assign")
	}
	if got[tag1].BuildID != buildID {
		t.Errorf("tag %s buildID = %q, want %q", tag1, got[tag1].BuildID, buildID)
	}

	multi, err := client.AssignTemplateTags(ctx, alias+":default", tag2, tag3)
	if err != nil {
		t.Fatalf("AssignTemplateTags(multi): %v", err)
	}
	if multi.BuildID != buildID {
		t.Errorf("multi BuildID = %q, want %q", multi.BuildID, buildID)
	}
	if len(multi.Tags) != 2 {
		t.Errorf("multi.Tags = %v, want 2 items", multi.Tags)
	}

	again, err := client.AssignTemplateTags(ctx, alias+":default", tag1)
	if err != nil {
		t.Fatalf("AssignTemplateTags(idempotent): %v", err)
	}
	if again.BuildID != buildID {
		t.Errorf("re-assign BuildID = %q, want %q", again.BuildID, buildID)
	}

	byBuild, err := client.AssignTemplateTags(ctx, alias+":"+buildID, tagBID)
	if err != nil {
		t.Fatalf("AssignTemplateTags(buildID): %v", err)
	}
	if byBuild.BuildID != buildID {
		t.Errorf("buildID assign BuildID = %q, want %q", byBuild.BuildID, buildID)
	}

	if len(tmpl.Names) > 0 {
		ns, err := client.AssignTemplateTags(ctx, tmpl.Names[0]+":default", tagNS)
		if err != nil {
			t.Fatalf("AssignTemplateTags(namespaced): %v", err)
		}
		if ns.BuildID != buildID {
			t.Errorf("namespaced BuildID = %q, want %q", ns.BuildID, buildID)
		}
	}

	if err := client.RemoveTemplateTags(ctx, alias, tag1, tag2, tag3, tagBID); err != nil {
		t.Fatalf("RemoveTemplateTags(batch): %v", err)
	}
	if len(tmpl.Names) > 0 {
		if err := client.RemoveTemplateTags(ctx, tmpl.Names[0], tagNS); err != nil {
			t.Fatalf("RemoveTemplateTags(namespaced): %v", err)
		}
	}

	if err := client.RemoveTemplateTags(ctx, alias, tag1); err != nil {
		t.Fatalf("RemoveTemplateTags(idempotent): %v", err)
	}
	if err := client.RemoveTemplateTags(ctx, alias, prefix+"-missing"); err != nil {
		t.Fatalf("RemoveTemplateTags(missing tag): %v", err)
	}

	after, err := client.ListTemplateTags(ctx, tmpl.TemplateID)
	if err != nil {
		t.Fatalf("ListTemplateTags after remove: %v", err)
	}
	for _, tag := range after {
		if strings.HasPrefix(tag.Tag, prefix) {
			t.Errorf("probe tag %q still present after remove", tag.Tag)
		}
	}
	if _, ok := tagSet(after)["default"]; !ok {
		t.Fatal("default tag was removed")
	}
}

func TestIntegrationAssignTemplateTagsNotFound(t *testing.T) {
	client := integrationClient(t)

	_, err := client.AssignTemplateTags(context.Background(), "no-such-template:default", "x")
	var nfe *TemplateNotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("expected *TemplateNotFoundError, got %T: %v", err, err)
	}
}

func TestIntegrationRemoveTemplateTagsNotFound(t *testing.T) {
	client := integrationClient(t)

	err := client.RemoveTemplateTags(context.Background(), "no-such-template", "x")
	var nfe *TemplateNotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("expected *TemplateNotFoundError, got %T: %v", err, err)
	}
}
