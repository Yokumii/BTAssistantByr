package topfree

import (
	"reflect"
	"testing"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func TestCandidatesWithinSizeLimit(t *testing.T) {
	t.Parallel()

	small := candidate(1, 49, 1, 1)
	exact := candidate(2, 50, 1, 1)
	large := candidate(3, 51, 1, 1)
	eligible, oversized, err := candidatesWithinSizeLimit([]domain.Candidate{large, exact, small}, 50)
	if err != nil {
		t.Fatalf("candidatesWithinSizeLimit() error = %v", err)
	}
	if !reflect.DeepEqual(eligible, []domain.Candidate{exact, small}) || !reflect.DeepEqual(oversized, []domain.Candidate{large}) {
		t.Fatalf("eligible = %+v, oversized = %+v", eligible, oversized)
	}

	unlimited, excluded, err := candidatesWithinSizeLimit([]domain.Candidate{large}, 0)
	if err != nil || len(unlimited) != 1 || len(excluded) != 0 {
		t.Fatalf("unlimited = %+v, excluded = %+v, error = %v", unlimited, excluded, err)
	}
}

func TestTagsInManagementScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tags []string
		want bool
	}{
		{name: "no tags", want: true},
		{name: "managed", tags: []string{ManagedTag}, want: true},
		{name: "deletion", tags: []string{DeleteTag}, want: true},
		{name: "both program tags", tags: []string{ManagedTag, DeleteTag}, want: true},
		{name: "blank entries", tags: []string{" ", ManagedTag}, want: true},
		{name: "manual tag", tags: []string{"keep"}},
		{name: "program and manual tags", tags: []string{ManagedTag, "keep"}},
		{name: "authorized deletion and manual tags", tags: []string{DeleteTag, "keep"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := tagsInManagementScope(test.tags); got != test.want {
				t.Errorf("tagsInManagementScope(%v) = %t, want %t", test.tags, got, test.want)
			}
		})
	}
}
