package alex

import "testing"

func TestParseCoursesFromCurrentMinifiedShape(t *testing.T) {
	script := `let t="/courses",i="system-design-interview",s="coding-patterns",u={` +
		`"system-design-interview":{title:"System Design Interview",authors:"Alex Xu",` +
		`claimCodes:["annual"],key:i,defaultChapter:` + "`${t}/${i}/scale-from-zero-to-millions-of-users`" +
		`,rootPath:` + "`${t}/${i}`" +
		`,lessons:30,students:25e4,showChapter:!0,lastModified:"2022.03.01"},` +
		`"coding-patterns":{title:"Coding Interview Patterns",authors:"Alex Xu",` +
		`claimCodes:[],key:s,defaultChapter:` + "`${t}/${s}/two-pointers/introduction-to-two-pointers`" +
		`,rootPath:` + "`${t}/${s}`" +
		`,lessons:101,students:1e4,showChapter:!1,lastModified:"2024.01.28"}}`

	courses := ParseCoursesFromScript(script)
	if len(courses) != 2 {
		t.Fatalf("course count = %d, want 2", len(courses))
	}
	systemDesign := courses["system-design-interview"]
	if systemDesign.DefaultChapter !=
		"/courses/system-design-interview/scale-from-zero-to-millions-of-users" {
		t.Fatalf("default chapter = %q", systemDesign.DefaultChapter)
	}
	if systemDesign.Students == nil || *systemDesign.Students != 250000 {
		t.Fatalf("students = %v, want 250000", systemDesign.Students)
	}
}

func TestValidCatalogScriptURLsAcceptsOnlyBoundedSameOriginDeployQuery(
	t *testing.T,
) {
	values := []string{
		"https://bytebytego.com/_next/static/chunks/app.js?dpl=deploy-1",
		"https://bytebytego.com/_next/static/chunks/plain.js",
		"https://evil.example/_next/static/chunks/app.js?dpl=deploy-1",
		"https://bytebytego.com/_next/static/chunks/app.js?x=1",
		"https://bytebytego.com/_next/static/chunks/app.js?dpl=one&dpl=two",
	}
	got := validCatalogScriptURLs(values)
	if len(got) != 2 {
		t.Fatalf("valid URL count = %d, want 2: %#v", len(got), got)
	}
}
