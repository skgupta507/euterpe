package library

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// Needed for tests as the go-sqlite3 must be imported during tests too.
	_ "github.com/mattn/go-sqlite3"

	"github.com/ironsmile/euterpe/src/helpers"
)

// testTimeout is the maximum time a test is allowed to work.
var testTimeout = 40 * time.Second

func contains(heystack []string, needle string) bool {
	for _, val := range heystack {
		if needle == val {
			return true
		}
	}
	return false
}

func containsInt64(heystack []int64, needle int64) bool {
	for _, val := range heystack {
		if needle == val {
			return true
		}
	}
	return false
}

func init() {
	// Will show the output from log in the console only
	// if the -v flag is passed to the tests.
	if !contains(os.Args, "-test.v=true") {
		devnull, _ := os.Create(os.DevNull)
		log.SetOutput(devnull)
	}
}

func getTestLibraryPath() (string, error) {
	projRoot, err := helpers.ProjectRoot()

	if err != nil {
		return "", err
	}

	return filepath.Join(projRoot, "test_files", "library"), nil
}

// It is the caller's responsibility to remove the library SQLite database file
func getLibrary(ctx context.Context, t *testing.T) *LocalLibrary {
	lib, err := NewLocalLibrary(ctx, SQLiteMemoryFile, getTestMigrationFiles())

	if err != nil {
		t.Fatal(err.Error())
	}

	err = lib.Initialize()

	if err != nil {
		t.Fatalf("Initializing library: %s", err)
	}

	testLibraryPath, err := getTestLibraryPath()

	if err != nil {
		t.Fatalf("Failed to get test library path: %s", err)
	}

	_ = lib.AddMedia(filepath.Join(testLibraryPath, "test_file_two.mp3"))
	_ = lib.AddMedia(filepath.Join(testLibraryPath, "folder_one", "third_file.mp3"))

	return lib
}

// It is the caller's responsibility to remove the library SQLite database file
func getPathedLibrary(ctx context.Context, t *testing.T) *LocalLibrary {
	projRoot, err := helpers.ProjectRoot()

	if err != nil {
		t.Fatalf("Was not able to find test_files directory: %s", err)
	}

	testLibraryPath := filepath.Join(projRoot, "test_files", "library")

	lib, err := NewLocalLibrary(ctx, SQLiteMemoryFile, getTestMigrationFiles())

	if err != nil {
		t.Fatal(err)
	}

	err = lib.Initialize()

	if err != nil {
		t.Fatalf("Initializing library: %s", err)
	}

	lib.AddLibraryPath(testLibraryPath)

	return lib
}

// It is the caller's responsibility to remove the library SQLite database file
func getScannedLibrary(ctx context.Context, t *testing.T) *LocalLibrary {
	lib := getPathedLibrary(ctx, t)

	ch := make(chan int)
	go func() {
		lib.Scan()
		ch <- 42
	}()

	testErrorAfter(t, 10*time.Second, ch, "Scanning library took too long")

	return lib
}

func testErrorAfter(t *testing.T, dur time.Duration, done chan int, message string) {
	select {
	case <-done:
	case <-time.After(dur):
		t.Errorf("Test timed out after %s: %s", dur, message)
		t.FailNow()
	}
}

func TestInitialize(t *testing.T) {
	libDB, err := os.CreateTemp("", "httpms_library_test_")

	if err != nil {
		t.Fatalf("Error creating temporary library: %s", err)
	}

	lib, err := NewLocalLibrary(context.Background(), libDB.Name(), getTestMigrationFiles())

	if err != nil {
		t.Fatal(err)
	}

	defer lib.Close()

	err = lib.Initialize()

	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = lib.Truncate()
		os.Remove(libDB.Name())
	}()

	st, err := os.Stat(libDB.Name())

	if err != nil {
		t.Fatal(err)
	}

	if st.Size() < 1 {
		t.Errorf("Library database was 0 bytes in size")
	}

	db, err := sql.Open("sqlite3", libDB.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var queries = []string{
		"SELECT count(id) as cnt FROM albums",
		"SELECT count(id) as cnt FROM tracks",
		"SELECT count(id) as cnt FROM artists",
	}

	for _, query := range queries {
		row, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer row.Close()
	}
}

func TestTruncate(t *testing.T) {
	libDB, err := os.CreateTemp("", "httpms_library_test_")

	if err != nil {
		t.Fatalf("Error creating temporary library: %s", err)
	}

	lib, err := NewLocalLibrary(context.TODO(), libDB.Name(), getTestMigrationFiles())

	if err != nil {
		t.Fatal(err)
	}

	err = lib.Initialize()

	if err != nil {
		t.Fatal(err)
	}

	_ = lib.Truncate()

	_, err = os.Stat(libDB.Name())

	if err == nil {
		os.Remove(libDB.Name())
		t.Errorf("Expected database file to be missing but it is still there")
	}
}

func TestSearch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getLibrary(ctx, t)
	defer func() {
		_ = lib.Truncate()
	}()

	found := lib.Search(ctx, SearchArgs{
		Query: "Buggy",
	})

	if len(found) != 1 {
		t.Fatalf("Expected 1 result but got %d", len(found))
	}

	expected := SearchResult{
		Artist:      "Buggy Bugoff",
		Album:       "Return Of The Bugs",
		Title:       "Payback",
		TrackNumber: 1,
	}

	if found[0].Artist != expected.Artist {
		t.Errorf("Expected Artist `%s` but found `%s`",
			expected.Artist, found[0].Artist)
	}

	if found[0].Title != expected.Title {
		t.Errorf("Expected Title `%s` but found `%s`",
			expected.Title, found[0].Title)
	}

	if found[0].Album != expected.Album {
		t.Errorf("Expected Album `%s` but found `%s`",
			expected.Album, found[0].Album)
	}

	if found[0].TrackNumber != expected.TrackNumber {
		t.Errorf("Expected TrackNumber `%d` but found `%d`",
			expected.TrackNumber, found[0].TrackNumber)
	}

	if found[0].AlbumID < 1 {
		t.Errorf("AlbumID was below 1: `%d`", found[0].AlbumID)
	}

}

func TestAddingNewFiles(t *testing.T) {

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	library := getLibrary(ctx, t)
	defer func() {
		_ = library.Truncate()
	}()

	tracksCount := func() int {
		rows, err := library.db.Query("SELECT count(id) as cnt FROM tracks")
		if err != nil {
			t.Fatal(err)
			return 0
		}
		defer rows.Close()

		var count int

		for rows.Next() {
			if err := rows.Scan(&count); err != nil {
				t.Errorf("error counting tracks: %s", err)
			}
		}

		return count
	}

	tracks := tracksCount()

	if tracks != 2 {
		t.Errorf("Expected to find 2 tracks but found %d", tracks)
	}

	projRoot, err := helpers.ProjectRoot()

	if err != nil {
		t.Fatal(err)
	}

	testLibraryPath := filepath.Join(projRoot, "test_files", "library")
	absentFile := filepath.Join(testLibraryPath, "not_there")

	err = library.AddMedia(absentFile)

	if err == nil {
		t.Fatalf("Expected a 'not found' error but got no error at all")
	}

	realFile := filepath.Join(testLibraryPath, "test_file_one.mp3")

	err = library.AddMedia(realFile)

	if err != nil {
		t.Error(err)
	}

	tracks = tracksCount()

	if tracks != 3 {
		t.Errorf("Expected to find 3 tracks but found %d", tracks)
	}

	found := library.Search(ctx, SearchArgs{Query: "Tittled Track"})

	if len(found) != 1 {
		t.Fatalf("Expected to find one track but found %d", len(found))
	}

	track := found[0]

	if track.Title != "Tittled Track" {
		t.Errorf("Found track had the wrong title: %s", track.Title)
	}
}

func TestAlbumFSPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	library := getLibrary(ctx, t)
	defer func() { _ = library.Truncate() }()

	testLibraryPath, err := getTestLibraryPath()

	if err != nil {
		t.Fatalf("Cannot get test library path: %s", testLibraryPath)
	}

	albumPaths, err := library.GetAlbumFSPathByName("Album Of Tests")

	if err != nil {
		t.Fatalf("Was not able to find Album Of Tests: %s", err)
	}

	if len(albumPaths) != 1 {
		t.Fatalf("Expected one path for an album but found %d", len(albumPaths))
	}

	if testLibraryPath != albumPaths[0] {
		t.Errorf("Album path mismatch. Expected `%s` but got `%s`", testLibraryPath,
			albumPaths[0])
	}
}

func TestPreAddedFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	library := getLibrary(ctx, t)
	defer func() { _ = library.Truncate() }()

	_, err := library.GetArtistID("doycho")

	if err == nil {
		t.Errorf("Was not expecting to find artist doycho")
	}

	artistID, err := library.GetArtistID("Artist Testoff")

	if err != nil {
		t.Fatalf("Was not able to find Artist Testoff: %s", err)
	}

	_, err = library.GetAlbumFSPathByName("Album Of Not Being There")

	if err == nil {
		t.Errorf("Was not expecting to find Album Of Not Being There but found one")
	}

	albumPaths, err := library.GetAlbumFSPathByName("Album Of Tests")

	if err != nil {
		t.Fatalf("Was not able to find Album Of Tests: %s", err)
	}

	if len(albumPaths) != 1 {
		t.Fatalf("Expected one path for an album but found %d", len(albumPaths))
	}

	albumID, err := library.GetAlbumID("Album Of Tests", albumPaths[0])

	if err != nil {
		t.Fatalf("Error gettin album by its name and FS path: %s", err)
	}

	_, err = library.GetTrackID("404 Not Found", artistID, albumID)

	if err == nil {
		t.Errorf("Was not expecting to find 404 Not Found track but it was there")
	}

	_, err = library.GetTrackID("Another One", artistID, albumID)

	if err != nil {
		t.Fatalf("Was not able to find track Another One: %s", err)
	}
}

func TestGettingAFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	library := getLibrary(ctx, t)
	defer func() { _ = library.Truncate() }()

	artistID, _ := library.GetArtistID("Artist Testoff")
	albumPaths, err := library.GetAlbumFSPathByName("Album Of Tests")

	if err != nil {
		t.Fatalf("Could not find album 'Album Of Tests': %s", err)
	}

	if len(albumPaths) != 1 {
		t.Fatalf("Expected 1 path for Album Of Tests but found %d", len(albumPaths))
	}

	albumID, err := library.GetAlbumID("Album Of Tests", albumPaths[0])

	if err != nil {
		t.Fatalf("Error getting album by its name and path: %s", err)
	}

	trackID, err := library.GetTrackID("Another One", artistID, albumID)

	if err != nil {
		t.Fatalf("File not found: %s", err)
	}

	filePath := library.GetFilePath(ctx, trackID)

	suffix := "/test_files/library/test_file_two.mp3"

	if !strings.HasSuffix(filePath, filepath.FromSlash(suffix)) {
		t.Errorf("Returned track file Another One did not have the proper file path")
	}
}

func TestAddingLibraryPaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getPathedLibrary(ctx, t)
	defer func() {
		_ = lib.Truncate()
	}()

	if len(lib.paths) != 1 {
		t.Fatalf("Expected 1 library path but found %d", len(lib.paths))
	}

	notExistingPath := filepath.FromSlash("/hopefully/not/existing/path/")

	lib.AddLibraryPath(notExistingPath)
	lib.AddLibraryPath(filepath.FromSlash("/"))

	if len(lib.paths) != 2 {
		t.Fatalf("Expected 2 library path but found %d", len(lib.paths))
	}
}

func TestScaning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getPathedLibrary(ctx, t)
	defer func() { _ = lib.Truncate() }()

	ch := make(chan int)
	go func() {
		lib.Scan()
		ch <- 42
	}()
	testErrorAfter(t, 10*time.Second, ch, "Scanning library took too long")

	for _, track := range []string{"Another One", "Payback", "Tittled Track"} {
		found := lib.Search(ctx, SearchArgs{Query: track})

		if len(found) != 1 {
			t.Errorf("%s was not found after the scan", track)
		}
	}
}

// TestRescanning alters a file in the database and then does a rescan, expecting
// its data to be synchronized back to what is on the filesystem.
func TestRescanning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getPathedLibrary(ctx, t)
	defer func() { _ = lib.Truncate() }()

	ch := make(chan int)
	go func() {
		lib.Scan()
		ch <- 42
	}()
	testErrorAfter(t, 10*time.Second, ch, "Scanning library took too long")

	const alterTrackQuery = `
		UPDATE tracks
		SET
			name = 'Broken File'
		WHERE
			name = 'Another One'
	`
	if _, err := lib.db.Exec(alterTrackQuery); err != nil {
		t.Fatalf("altering track in the database failed")
	}

	var rescanErr error
	go func() {
		rescanErr = lib.Rescan(ctx)
		ch <- 42
	}()
	testErrorAfter(t, 10*time.Second, ch, "Rescanning library took too long")

	if rescanErr != nil {
		t.Fatalf("rescan returned an error: %s", rescanErr)
	}

	for _, track := range []string{"Another One", "Payback", "Tittled Track"} {
		found := lib.Search(ctx, SearchArgs{Query: track})

		if len(found) != 1 {
			t.Errorf("%s was not found after the scan", track)
		}
	}
}

func TestSQLInjections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getScannedLibrary(ctx, t)
	defer func() { _ = lib.Truncate() }()

	found := lib.Search(ctx, SearchArgs{
		Query: `not-such-thing" OR 1=1 OR t.name="kleopatra`,
	})

	if len(found) != 0 {
		t.Errorf("Successful sql injection in a single query")
	}
}

func TestGetAlbumFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getScannedLibrary(ctx, t)
	defer func() { _ = lib.Truncate() }()

	albumPaths, err := lib.GetAlbumFSPathByName("Album Of Tests")

	if err != nil {
		t.Fatalf("Could not find fs paths for 'Album Of Tests' album: %s", err)
	}

	albumID, _ := lib.GetAlbumID("Album Of Tests", albumPaths[0])
	albumFiles := lib.GetAlbumFiles(ctx, albumID)

	if len(albumFiles) != 2 {
		t.Errorf("Expected 2 files in the album but found %d", len(albumFiles))
	}

	for _, track := range albumFiles {
		if track.Album != "Album Of Tests" {
			t.Errorf("GetAlbumFiles returned file in album `%s`", track.Album)
		}

		if track.Artist != "Artist Testoff" {
			t.Errorf("GetAlbumFiles returned file from artist `%s`", track.Artist)
		}
	}

	trackNames := []string{"Tittled Track", "Another One"}

	for _, trackName := range trackNames {
		found := false
		for _, track := range albumFiles {
			if track.Title == trackName {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("Track `%s` was not among the results", trackName)
		}
	}
}

func TestRemoveFileFunction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getScannedLibrary(ctx, t)
	defer func() { _ = lib.Truncate() }()

	found := lib.Search(ctx, SearchArgs{Query: "Another One"})

	if len(found) != 1 {
		t.Fatalf(`Expected searching for 'Another One' to return one `+
			`result but they were %d`, len(found))
	}

	fsPath := lib.GetFilePath(ctx, found[0].ID)

	lib.removeFile(fsPath)

	found = lib.Search(ctx, SearchArgs{Query: "Another One"})

	if len(found) != 0 {
		t.Error(`Did not expect to find Another One but it was there.`)
	}
}

func checkAddedSong(ctx context.Context, lib *LocalLibrary, t *testing.T) {
	found := lib.Search(ctx, SearchArgs{Query: "Added Song"})

	if len(found) != 1 {
		filePaths := []string{}
		for _, track := range found {
			filePath := lib.GetFilePath(ctx, track.ID)
			filePaths = append(filePaths, fmt.Sprintf("%d: %s", track.ID, filePath))
		}
		t.Fatalf("Expected one result, got %d for Added Song: %+v. Paths:\n%s",
			len(found), found, strings.Join(filePaths, "\n"))
	}

	track := found[0]

	if track.Album != "Unexpected Album" {
		t.Errorf("Wrong track album: %s", track.Album)
	}

	if track.Artist != "New Artist 2" {
		t.Errorf("Wrong track artist: %s", track.Artist)
	}

	if track.Title != "Added Song" {
		t.Errorf("Wrong track title: %s", track.Title)
	}

	if track.TrackNumber != 1 {
		t.Errorf("Wrong track number: %d", track.TrackNumber)
	}
}

func checkSong(ctx context.Context, lib *LocalLibrary, song MediaFile, t *testing.T) {
	found := lib.Search(ctx, SearchArgs{Query: song.Title()})

	if len(found) != 1 {
		t.Fatalf("Expected one result, got %d for %s: %+v",
			len(found), song.Title(), found)
	}

	track := found[0]

	if track.Album != song.Album() {
		t.Errorf("Wrong track album: %s when expecting %s", track.Album, song.Album())
	}

	if track.Artist != song.Artist() {
		t.Errorf("Wrong track artist: %s when expecting %s", track.Artist, song.Artist())
	}

	if track.Title != song.Title() {
		t.Errorf("Wrong track title: %s when expecting %s", track.Title, song.Title())
	}

	if track.TrackNumber != int64(song.Track()) {
		t.Errorf("Wrong track: %d when expecting %d", track.TrackNumber, song.Track())
	}
}

func TestAddingManyFilesSimultaniously(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getPathedLibrary(ctx, t)
	defer func() { _ = lib.Truncate() }()

	numberOfFiles := 100
	mediaFiles := make([]MediaFile, 0, numberOfFiles)

	for i := 0; i < numberOfFiles; i++ {
		m := &MockMedia{
			artist: fmt.Sprintf("artist %d", i),
			album:  fmt.Sprintf("album %d", i),
			title:  fmt.Sprintf("title %d full", i),
			track:  i,
			length: 123 * time.Second,
		}
		mInfo := fileInfo{
			Size:     int64(m.Length().Seconds()) * 256000,
			FilePath: fmt.Sprintf("/path/to/file_%d", i),
			Modified: time.Now(),
		}

		if err := lib.insertMediaIntoDatabase(m, mInfo); err != nil {
			t.Fatalf("Error adding media into the database: %s", err)
		}

		mediaFiles = append(mediaFiles, m)
	}

	for _, song := range mediaFiles {
		checkSong(ctx, lib, song, t)
	}
}

// TestAlbumsWithDifferentArtists simulates an album which has different artists.
// This album must have the same album ID since all of the tracks are in the same
// directory and the same album name.
func TestAlbumsWithDifferentArtists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getPathedLibrary(ctx, t)
	defer func() { _ = lib.Truncate() }()

	var err error

	tracks := []MockMedia{
		{
			artist: "Buggy Bugoff",
			album:  "Return Of The Bugs",
			title:  "Payback",
			track:  1,
			length: 340 * time.Second,
		},
		{
			artist: "Buggy Bugoff",
			album:  "Return Of The Bugs",
			title:  "Realization",
			track:  2,
			length: 345 * time.Second,
		},
		{
			artist: "Off By One",
			album:  "Return Of The Bugs",
			title:  "Index By Index",
			track:  3,
			length: 244 * time.Second,
		},
	}

	for _, track := range tracks {
		trackInfo := fileInfo{
			Size:     int64(track.Length().Seconds()) * 256000,
			FilePath: fmt.Sprintf("/media/return-of-the-bugs/%s.mp3", track.Title()),
			Modified: time.Now(),
		}
		err = lib.insertMediaIntoDatabase(&track, trackInfo)
		if err != nil {
			t.Fatalf("Adding a media file %s failed: %s", track.Title(), err)
		}
	}

	found := lib.Search(ctx, SearchArgs{Query: "Return Of The Bugs"})

	if len(found) != 3 {
		t.Fatalf("Expected to find 3 tracks but found %d", len(found))
	}

	albumID := found[0].AlbumID
	albumName := found[0].Album

	for _, foundTrack := range found {
		if foundTrack.AlbumID != albumID {
			t.Errorf("Track %s had a different album id in db", foundTrack.Title)
		}

		if foundTrack.Album != albumName {
			t.Errorf(
				"Track %s had a different album name: %s",
				foundTrack.Title,
				foundTrack.Album,
			)
		}
	}
}

// TestDifferentAlbumsWithTheSameName makes sure that albums with the same name which
// are for different artists should have different IDs when the album is in a different
// directory.
func TestDifferentAlbumsWithTheSameName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getPathedLibrary(ctx, t)
	defer func() { _ = lib.Truncate() }()

	tracks := []struct {
		track MockMedia
		path  string
	}{
		{
			track: MockMedia{
				artist: "Buggy Bugoff",
				album:  "Return Of The Bugs",
				title:  "Payback",
				track:  1,
				length: 340 * time.Second,
			},
			path: "/media/return-of-the-bugs/track-1.mp3",
		},
		{
			track: MockMedia{
				artist: "Buggy Bugoff",
				album:  "Return Of The Bugs",
				title:  "Realization",
				track:  2,
				length: 345 * time.Second,
			},
			path: "/media/return-of-the-bugs/track-2.mp3",
		},
		{
			track: MockMedia{
				artist: "Off By One",
				album:  "Return Of The Bugs",
				title:  "Index By Index",
				track:  1,
				length: 244 * time.Second,
			},
			path: "/media/second-return-of-the-bugs/track-1.mp3", // different directory
		},
	}

	for _, trackData := range tracks {
		fileInfo := fileInfo{
			Size:     int64(trackData.track.Length().Seconds()) * 256000,
			FilePath: trackData.path,
			Modified: time.Now(),
		}
		err := lib.insertMediaIntoDatabase(&trackData.track, fileInfo)

		if err != nil {
			t.Fatalf("Adding a media file %s failed: %s", trackData.track.Title(), err)
		}
	}

	found := lib.Search(ctx, SearchArgs{Query: "Return Of The Bugs"})

	if len(found) != 3 {
		t.Errorf("Expected to find 3 tracks but found %d", len(found))
	}

	albumIDs := make([]int64, 0, 2)

	for _, track := range found {
		if containsInt64(albumIDs, track.AlbumID) {
			continue
		}
		albumIDs = append(albumIDs, track.AlbumID)
	}

	if len(albumIDs) != 2 {
		t.Errorf(
			"There should have been two 'Return Of The Bugs' albums but there were %d",
			len(albumIDs),
		)
	}
}

// TestLocalLibrarySupportedFormats makes sure that format recognition from file name
// does return true only for supported formats.
func TestLocalLibrarySupportedFormats(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{
			path:     filepath.FromSlash("some/path.mp3"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("path.mp3"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("some/path.ogg"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("some/path.wav"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("some/path.fla"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("some/path.flac"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("path.flac"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("some/.mp3"),
			expected: false,
		},
		{
			path:     filepath.FromSlash("file.MP3"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("some/file.pdf"),
			expected: false,
		},
		{
			path:     filepath.FromSlash("some/mp3"),
			expected: false,
		},
		{
			path:     filepath.FromSlash("mp3"),
			expected: false,
		},
		{
			path:     filepath.FromSlash("somewhere/file.opus"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("somewhere/FILE.webm"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("somewhere/other.WEbm"),
			expected: true,
		},
		{
			path:     filepath.FromSlash("/proc/cpuinfo"),
			expected: false,
		},
	}

	// lib does not need to be initialized. The isSupportedFormat method does not
	// touch any of its properties.
	lib := LocalLibrary{}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			actual := lib.isSupportedFormat(test.path)
			if test.expected != actual {
				t.Errorf("Support for %s is wrong. Expected %t but got %t.",
					test.path, test.expected, actual)
			}
		})
	}
}

// TestLocalLibraryGetArtistAlbums makes sure that the LocalLibrary's GetArtistAlbums
// returns the expected results.
func TestLocalLibraryGetArtistAlbums(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lib := getPathedLibrary(ctx, t)
	defer func() { _ = lib.Truncate() }()

	const (
		albumName       = "Return Of The Bugs"
		secondAlbumName = "Return Of The Bugs II Deluxe 3000"
		artistName      = "Buggy Bugoff"
	)

	tracks := []struct {
		track MockMedia
		path  string
	}{
		{
			track: MockMedia{
				artist: artistName,
				album:  albumName,
				title:  "Payback",
				track:  1,
				length: 340 * time.Second,
			},
			path: "/media/return-of-the-bugs/track-1.mp3",
		},
		{
			track: MockMedia{
				artist: artistName,
				album:  albumName,
				title:  "Realization",
				track:  2,
				length: 345 * time.Second,
			},
			path: "/media/return-of-the-bugs/track-2.mp3",
		},
		{
			track: MockMedia{
				artist: artistName,
				album:  secondAlbumName,
				title:  "Index By Index",
				track:  1,
				length: 244 * time.Second,
			},
			path: "/media/second-return-of-the-bugs/track-1.mp3",
		},
		{
			track: MockMedia{
				artist: "Nothing To Do With The Rest",
				album:  "Maybe Some Less Bugs Please",
				title:  "Test By Test",
				track:  1,
				length: 523 * time.Second,
			},
			path: "/media/maybe-some-less-bugs/track-1.mp3",
		},
	}

	expected := map[string]Album{
		albumName: {
			Name:   albumName,
			Artist: artistName,
		},
		secondAlbumName: {
			Name:   secondAlbumName,
			Artist: artistName,
		},
	}

	for _, trackData := range tracks {
		trackInfo := fileInfo{
			Size:     int64(trackData.track.Length().Seconds()) * 256000,
			FilePath: trackData.path,
			Modified: time.Now(),
		}
		err := lib.insertMediaIntoDatabase(&trackData.track, trackInfo)

		if err != nil {
			t.Fatalf("Adding a media file %s failed: %s", trackData.track.Title(), err)
		}

		al, ok := expected[trackData.track.album]
		if !ok {
			continue
		}

		al.Duration += trackData.track.length.Milliseconds()
		al.SongCount++

		expected[al.Name] = al
	}

	var artistID int64
	results := lib.Search(ctx, SearchArgs{Query: artistName})
	for _, track := range results {
		if track.Artist == artistName {
			artistID = track.ArtistID
			break
		}
	}

	if artistID == 0 {
		t.Fatalf("could not find artist `%s`", artistName)
	}

	artistAlbums := lib.GetArtistAlbums(ctx, artistID)
	if len(expected) != len(artistAlbums) {
		t.Errorf("expected %d albums but got %d", len(expected), len(artistAlbums))
	}

	for _, expectedAlbum := range expected {
		var found bool
		for _, album := range artistAlbums {
			if album.Name != expectedAlbum.Name || album.Artist != expectedAlbum.Artist {
				continue
			}
			found = true

			if expectedAlbum.SongCount != album.SongCount {
				t.Errorf("album `%s`: expected %d songs but got %d",
					album.Name,
					expectedAlbum.SongCount,
					album.SongCount,
				)
			}

			if expectedAlbum.Duration != album.Duration {
				t.Errorf("album `%s`: expected %dms duration but got %dms",
					album.Name,
					expectedAlbum.Duration,
					album.Duration,
				)
			}
		}

		if !found {
			t.Errorf("Album `%s` was not found among the artist albums",
				expectedAlbum.Name,
			)
		}
	}
}

// getTestMigrationFiles returns the SQLs directory used by the application itself
// normally. This way tests will be done with the exact same files which will be
// bundled into the binary on build.
func getTestMigrationFiles() fs.FS {
	return os.DirFS("../../sqls")
}
