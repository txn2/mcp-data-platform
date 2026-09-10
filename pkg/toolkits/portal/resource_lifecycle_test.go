package portal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeHolds answers what points at a file.
type fakeHolds struct {
	holds     ResourceHolds
	askedFor  string
	returnErr error
}

func (f *fakeHolds) ResourceHolds(_ context.Context, resourceID string) (ResourceHolds, error) {
	f.askedFor = resourceID
	if f.returnErr != nil {
		return ResourceHolds{}, f.returnErr
	}
	return f.holds, nil
}

// lifecycleToolkit is a toolkit with a bound writer and a bound hold reader,
// which is what a deployment with an asset, prompt and knowledge layer has.
func lifecycleToolkit(t *testing.T) (*Toolkit, *fakeResourceWriter, *fakeHolds) {
	t.Helper()
	tk, w := resourceToolkit(t)
	h := &fakeHolds{}
	tk.SetResourceHolds(h)
	return tk, w, h
}

func decodeGet(t *testing.T, result *mcp.CallToolResult) resourceGetOutput {
	t.Helper()
	require.False(t, result.IsError, "call failed: %s", errText(t, result))
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out resourceGetOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

func decodeList(t *testing.T, result *mcp.CallToolResult) resourceListOutput {
	t.Helper()
	require.False(t, result.IsError, "call failed: %s", errText(t, result))
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out resourceListOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

func decodeDelete(t *testing.T, result *mcp.CallToolResult) resourceDeleteOutput {
	t.Helper()
	require.False(t, result.IsError, "call failed: %s", errText(t, result))
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out resourceDeleteOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

// --- get ---

func TestGetAnswersWhatIsFiledAtAnAddress(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.located = w.existingOrDefault()

	out := decodeGet(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionGet, Path: "datasets", Filename: "weather.csv",
	}))

	assert.True(t, out.Found)
	require.NotNil(t, out.Resource)
	assert.Equal(t, "res1", out.Resource.ResourceID)
	assert.Equal(t, "mcp:resource:res1", out.Resource.Reference,
		"the lookup hands the caller the reference every other tool takes")
	assert.Equal(t, "mcp://user/user1/datasets/weather.csv", out.Resource.URI)
	assert.Equal(t, "datasets", w.locatedAddr.Path, "the address the caller named is what was looked up")
	assert.Equal(t, "weather.csv", w.locatedAddr.Filename)
}

func TestGetReportsAnEmptyAddressAndHowToFillIt(t *testing.T) {
	tk, _, _ := lifecycleToolkit(t)

	out := decodeGet(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionGet, Path: "datasets", Filename: "absent.csv",
	}))

	assert.False(t, out.Found)
	assert.Nil(t, out.Resource)
	assert.Equal(t, "mcp://user//datasets/absent.csv", out.URI, "the address is named even when it is empty")
	assert.Contains(t, out.Message, "if_exists=replace", "the answer names the way to write there")
}

func TestGetByReference(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)

	out := decodeGet(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionGet, Reference: "mcp:resource:res1",
	}))

	assert.True(t, out.Found)
	assert.Equal(t, "res1", w.gotID)
	assert.Contains(t, out.Message, "fetch", "the bytes are somewhere else and the answer says where")
}

func TestGetRefusesAReferenceToSomethingElse(t *testing.T) {
	tk, _, _ := lifecycleToolkit(t)

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionGet, Reference: "mcp:asset:a1",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "manage_asset")
}

func TestGetWithNeitherAReferenceNorAnAddress(t *testing.T) {
	tk, _, _ := lifecycleToolkit(t)

	result := callResource(t, tk, manageResourceInput{Action: resourceActionGet})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "name the file to act on")
	assert.Contains(t, errText(t, result), "scope, path and filename")
}

func TestGetReportsAFailedLookup(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.locateErr = errors.New("could not read what is filed at mcp://user/user1/datasets/weather.csv")

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionGet, Path: "datasets", Filename: "weather.csv",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "could not read what is filed at")
}

// --- list ---

func TestListReportsTheFolderAndItsPaging(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.listed = []resource.Resource{
		{ID: "res1", Path: "datasets", Filename: "a.csv", URI: "mcp://user/user1/datasets/a.csv"},
		{ID: "res2", Path: "datasets/weather", Filename: "b.csv", URI: "mcp://user/user1/datasets/weather/b.csv"},
	}
	w.listTotal = 5

	out := decodeList(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionList, Path: "datasets", Limit: 2,
	}))

	assert.Equal(t, "datasets", out.Path)
	require.Len(t, out.Resources, 2)
	assert.Equal(t, "mcp:resource:res1", out.Resources[0].Reference)
	assert.Equal(t, 5, out.Total)
	assert.Contains(t, out.Message, "offset=2", "an answer that was cut says how to ask for the rest")
	assert.Equal(t, "datasets", w.listQuery.Path)
	assert.Equal(t, 2, w.listQuery.Limit)
}

func TestListWithNoPathSaysItIsTheWholeLibrary(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.listed, w.listTotal = []resource.Resource{}, 0

	out := decodeList(t, callResource(t, tk, manageResourceInput{Action: resourceActionList}))

	assert.Contains(t, out.Message, "whole library")
	assert.Empty(t, out.Resources)
}

func TestListReportsAFailedRead(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.listErr = errors.New("could not list the managed resources")

	result := callResource(t, tk, manageResourceInput{Action: resourceActionList, Path: "datasets"})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "could not list")
}

// --- delete ---

func TestDeleteRemovesAFileNothingPointsAt(t *testing.T) {
	tk, w, h := lifecycleToolkit(t)
	reg := &fakeTableRegistrar{}
	tk.SetTableRegistrar(reg)
	w.located = w.existingOrDefault()

	out := decodeDelete(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
	}))

	assert.True(t, out.Deleted)
	assert.Equal(t, "res1", out.ResourceID)
	assert.Equal(t, "res1", w.deletedID)
	assert.Equal(t, "res1", h.askedFor)
	assert.Equal(t, []string{"res1"}, reg.droppedResources,
		"a table over a file that is gone would answer queries from a location nothing owns")
	assert.Contains(t, out.Message, "resolve to nothing")
}

func TestDeleteByReference(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)

	out := decodeDelete(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Reference: "mcp:resource:res1",
	}))

	assert.True(t, out.Deleted)
	assert.Equal(t, "res1", w.deletedID)
}

func TestDeleteRefusesWhileSomethingPointsAtTheFile(t *testing.T) {
	tk, w, h := lifecycleToolkit(t)
	w.located = w.existingOrDefault()
	h.holds = ResourceHolds{Assets: 2, Prompts: 1}

	out := decodeDelete(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
	}))

	assert.False(t, out.Deleted)
	assert.Empty(t, w.deletedID, "the refusal happens before the delete, not after it")
	require.NotNil(t, out.Holds)
	assert.Equal(t, 2, out.Holds.Assets)
	assert.Contains(t, out.Message, "2 assets reference this file")
	assert.Contains(t, out.Message, "1 prompt attaches this file")
	assert.Contains(t, out.Message, "force=true", "a refusal that names no way through is a dead end")
}

func TestDeleteRefusesWhileATableIsRegisteredOverTheFile(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	reg := &fakeTableRegistrar{registered: []TableRegistration{
		{RegistrationID: "reg1", QueryTable: "scratch.uploads.weather"},
	}}
	tk.SetTableRegistrar(reg)
	w.located = w.existingOrDefault()

	out := decodeDelete(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
	}))

	assert.False(t, out.Deleted)
	assert.Equal(t, []string{"scratch.uploads.weather"}, out.Tables,
		"a table is named where the other holders are counted, because manage_table already names it")
	assert.Contains(t, out.Message, "scratch.uploads.weather")
	assert.Empty(t, reg.droppedResources)
}

func TestDeleteForcedPastWhatPointsAtTheFile(t *testing.T) {
	tk, w, h := lifecycleToolkit(t)
	reg := &fakeTableRegistrar{registered: []TableRegistration{
		{RegistrationID: "reg1", QueryTable: "scratch.uploads.weather"},
	}}
	tk.SetTableRegistrar(reg)
	w.located = w.existingOrDefault()
	h.holds = ResourceHolds{Assets: 1}

	out := decodeDelete(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv", Force: true,
	}))

	assert.True(t, out.Deleted)
	assert.Equal(t, "res1", w.deletedID)
	require.NotNil(t, out.Holds)
	assert.Equal(t, 1, out.Holds.Assets, "a forced delete records what the caller chose to break")
	assert.Contains(t, out.Message, "points at a file that is not there")
	assert.Equal(t, []string{"res1"}, reg.droppedResources)
}

func TestDeleteWillNotGuessWhenItCannotEstablishWhatDependsOnTheFile(t *testing.T) {
	t.Run("no reader bound", func(t *testing.T) {
		tk, w := resourceToolkit(t)
		w.located = w.existingOrDefault()

		result := callResource(t, tk, manageResourceInput{
			Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
		})

		require.True(t, result.IsError)
		assert.Contains(t, errText(t, result), "force=true")
		assert.Empty(t, w.deletedID)
	})

	t.Run("the reader failed", func(t *testing.T) {
		tk, w, h := lifecycleToolkit(t)
		w.located = w.existingOrDefault()
		h.returnErr = errors.New("connection reset")

		result := callResource(t, tk, manageResourceInput{
			Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
		})

		require.True(t, result.IsError)
		assert.Contains(t, errText(t, result), "could not establish what depends on this file")
		assert.Empty(t, w.deletedID, "a lookup that failed must not read as nothing depends on it")
	})

	t.Run("forced past a reader that failed", func(t *testing.T) {
		tk, w, h := lifecycleToolkit(t)
		w.located = w.existingOrDefault()
		h.returnErr = errors.New("connection reset")

		out := decodeDelete(t, callResource(t, tk, manageResourceInput{
			Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv", Force: true,
		}))

		assert.True(t, out.Deleted, "the caller already decided; a count that could not be read must not stop them")
		assert.Nil(t, out.Holds)
	})
}

func TestDeleteOfAnAddressThatHoldsNothing(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "absent.csv",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "Nothing was deleted")
	assert.Empty(t, w.deletedID)
}

func TestDeleteReportsARefusedDelete(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.located = w.existingOrDefault()
	w.deleteErr = errors.New("you cannot delete a file in the global scope, which is administrators only")

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "cannot delete a file in the global scope")
}

// --- create with if_exists ---

func TestCreateWithIfExistsReplaceWritesTheNextVersion(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.located = w.existingOrDefault()

	input := createInputFor()
	input.IfExists = ifExistsReplace
	out := decodeResourceOutput(t, callResource(t, tk, input))

	assert.Equal(t, "res1", w.gotID, "the create found the address taken and revised what was there")
	assert.Equal(t, 2, out.Version)
	assert.Empty(t, w.created.Filename, "nothing was created")
	assert.Equal(t, "day,high\nmon,71\ntue,68\n", string(w.replacedContent))
}

func TestCreateWithIfExistsReplaceCreatesWhenTheAddressIsEmpty(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)

	input := createInputFor()
	input.IfExists = ifExistsReplace
	out := decodeResourceOutput(t, callResource(t, tk, input))

	assert.Equal(t, "weather-daily.csv", w.created.Filename, "an empty address is a create")
	assert.Zero(t, out.Version)
}

func TestCreateDefaultsToRefusingAnAddressThatIsTaken(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.located = w.existingOrDefault()
	w.createErr = errors.New("a file with this name already exists in this folder")

	result := callResource(t, tk, createInputFor())

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "already exists")
	assert.Empty(t, w.gotID, "without if_exists=replace the create never looked for what was there")
}

func TestCreateRefusesAnIfExistsItDoesNotTake(t *testing.T) {
	tk, _, _ := lifecycleToolkit(t)

	input := createInputFor()
	input.IfExists = "overwrite"
	result := callResource(t, tk, input)

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "is not one this tool takes")
	assert.Contains(t, errText(t, result), "replace")
}

func TestCreateWithIfExistsReplaceReportsAFailedLookup(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.locateErr = errors.New("could not read what is filed at mcp://user/user1/datasets/weather-daily.csv")

	input := createInputFor()
	input.IfExists = ifExistsReplace
	result := callResource(t, tk, input)

	require.True(t, result.IsError)
	assert.Empty(t, w.created.Filename,
		"creating on a failed read would file a second file at an address that already has one")
}

// --- the sentences a refusal is built from ---

func TestResourceHoldsDescribe(t *testing.T) {
	assert.Empty(t, ResourceHolds{}.Describe())
	assert.False(t, ResourceHolds{}.Any())
	assert.Equal(t,
		[]string{"1 asset references this file", "2 prompts attach this file"},
		ResourceHolds{Assets: 1, Prompts: 2}.Describe())
	assert.True(t, ResourceHolds{Prompts: 1}.Any())
}

func TestDeleteSaysWhenMorePointAtTheFileThanWereCounted(t *testing.T) {
	tk, w, h := lifecycleToolkit(t)
	w.located = w.existingOrDefault()
	h.holds = ResourceHolds{Assets: 50, More: true}

	out := decodeDelete(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
	}))

	assert.False(t, out.Deleted)
	assert.Contains(t, out.Message, "More point at it than were counted.")
}

func TestDeleteNamesSeveralTablesAtOnce(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	tk.SetTableRegistrar(&fakeTableRegistrar{registered: []TableRegistration{
		{QueryTable: "scratch.uploads.a"}, {QueryTable: "scratch.uploads.b"},
	}})
	w.located = w.existingOrDefault()

	out := decodeDelete(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
	}))

	assert.False(t, out.Deleted)
	assert.Contains(t, out.Message, "2 tables are registered over it: scratch.uploads.a, scratch.uploads.b")
}

func TestDeleteRefusesAReferenceThatIsNotOne(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Reference: "not-a-reference",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "not a reference this platform issues")
	assert.Empty(t, w.deletedID)
}

func TestDeleteByAReferenceNamingAFileThatIsGone(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.getErr = errors.New("there is no managed resource \"res1\" you can see")

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Reference: "mcp:resource:res1",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "no managed resource")
	assert.Empty(t, w.deletedID)
}

func TestDeleteTreatsAnUnreadableRegistrarAsNoTables(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	tk.SetTableRegistrar(&fakeTableRegistrar{listErr: errors.New("registration store unreachable")})
	w.located = w.existingOrDefault()

	out := decodeDelete(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
	}))

	assert.True(t, out.Deleted, "the table half degrades; the three that would break silently do not")
	assert.Empty(t, out.Tables)
}

func TestGetByAReferenceNamingAFileThatIsGone(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.getErr = errors.New("there is no managed resource \"res1\" you can see")

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionGet, Reference: "mcp:resource:res1",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "no managed resource")
}

func TestDeleteWithNeitherAReferenceNorAnAddress(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)

	result := callResource(t, tk, manageResourceInput{Action: resourceActionDelete})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "name the file to act on")
	assert.Empty(t, w.deletedID)
}

func TestDeleteReportsAFailedLookup(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	w.locateErr = errors.New("could not read what is filed at mcp://user/user1/datasets/weather.csv")

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "could not read what is filed at")
	assert.Empty(t, w.deletedID, "a delete on a failed read would remove a file nobody established was there")
}

func TestForcedDeleteNamesEveryTableItTookDown(t *testing.T) {
	tk, w, _ := lifecycleToolkit(t)
	reg := &fakeTableRegistrar{registered: []TableRegistration{
		{QueryTable: "scratch.uploads.a"}, {QueryTable: "scratch.uploads.b"},
	}}
	tk.SetTableRegistrar(reg)
	w.located = w.existingOrDefault()

	out := decodeDelete(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionDelete, Path: "datasets", Filename: "weather.csv", Force: true,
	}))

	assert.True(t, out.Deleted)
	assert.Contains(t, out.Message, "The 2 tables registered over it were unregistered with it: "+
		"scratch.uploads.a, scratch.uploads.b.")
	assert.Equal(t, []string{"res1"}, reg.droppedResources)
}
