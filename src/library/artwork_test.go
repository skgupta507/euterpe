package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ironsmile/euterpe/src/art"
	"github.com/ironsmile/euterpe/src/art/artfakes"
	"github.com/ironsmile/euterpe/src/scaler/scalerfakes"
)

// TestFindAndSaveAlbumArtwork checks that album artwork is stored and then searches
// by the following mechanism:
//
//	* First try the database
//	* Then the file system
//	* Finally make an request with the art.Finder
//
func TestFindAndSaveAlbumArtwork(t *testing.T) {
	var (
		bigImage       = []byte("big-image-is-really-bigger-than-the-small")
		secondBigImage = []byte("second-album-original-image")
		smallImage     = []byte("small-image")
		ctx            = context.Background()
		mediaFile      = MockMedia{
			artist: "Testy Testov",
			album:  "The Test Strikes Back",
			title:  "One Final Bug",
			track:  1,
			length: 334,
		}
	)

	lib, err := NewLocalLibrary(ctx, SQLiteMemoryFile, getTestMigrationFiles())
	if err != nil {
		t.Fatalf(err.Error())
	}

	if err := lib.Initialize(); err != nil {
		t.Fatalf("Initializing library: %s", err)
	}

	defer func() { _ = lib.Truncate() }()

	fakeAF := &artfakes.FakeFinder{
		GetFrontImageStub: func(
			_ context.Context,
			artist string,
			album string,
		) ([]byte, error) {
			if artist != mediaFile.artist || album != mediaFile.album {
				return nil, art.ErrImageNotFound
			}

			retSlice := make([]byte, len(bigImage))
			copy(retSlice, bigImage)

			return retSlice, nil
		},
	}
	lib.SetArtFinder(fakeAF)

	fakeScaler := &scalerfakes.FakeScaler{
		ScaleStub: func(ctx context.Context, r io.Reader, toWidth int) ([]byte, error) {
			if toWidth != 60 {
				return nil, fmt.Errorf("expected to scale to size 60")
			}

			inputBytes, err := ioutil.ReadAll(r)
			if err != nil {
				return nil, fmt.Errorf("reading input image: %s", err)
			}

			if len(inputBytes) < 1 {
				return nil, fmt.Errorf("input image is empty")
			}

			if !bytes.Equal(bigImage, inputBytes) &&
				!bytes.Equal(secondBigImage, inputBytes) {
				return nil, fmt.Errorf(
					"expected to resize one of the big images but it was `%s`",
					inputBytes,
				)
			}

			imgb := make([]byte, len(smallImage))
			copy(imgb, smallImage)
			return imgb, nil
		},
	}
	lib.SetScaler(fakeScaler)

	const (
		mediaFilePath   = "path/to/albums/1/first.mp3"
		secondFilePath  = "path/to/albums/2/second.mp3"
		thirdFilePath   = "path/to/albums/3/third.mp3"
		thirdAlbumCover = "expected-cover-file-contents"
	)
	mapfs := fstest.MapFS{
		mediaFilePath: &fstest.MapFile{
			Data:    []byte("some-file"),
			ModTime: time.Now(),
		},
		thirdFilePath: &fstest.MapFile{
			Data:    []byte("third-file"),
			ModTime: time.Now(),
		},
		"path/to/albums/3/inner/cover.png": &fstest.MapFile{
			Data:    []byte("inner/cover.png"),
			ModTime: time.Now(),
		},
		"path/to/albums/3/.cover.png": &fstest.MapFile{
			Data:    []byte(".cover.png"),
			ModTime: time.Now(),
		},
		"path/to/albums/3/cover-me-baby.jpeg": &fstest.MapFile{
			Data:    []byte("cover-me-baby.jpeg"),
			ModTime: time.Now(),
		},
		"path/to/albums/3/some-artwork-here.jpg": &fstest.MapFile{
			Data:    []byte("some-artwork-here.jpg"),
			ModTime: time.Now(),
		},
		"path/to/albums/3/cover.png": &fstest.MapFile{
			Data:    []byte(thirdAlbumCover),
			ModTime: time.Now(),
		},
	}

	lib.fs = mapfs

	if err := lib.insertMediaIntoDatabase(&mediaFile, mediaFilePath); err != nil {
		t.Fatalf("inserting media file failed: %s", err)
	}

	// Set-up finished. Actual tests start here. First try to find an image for
	// an album which does not have one in the database.
	assertAlbumImage(t, lib, 1, SmallImage, smallImage)

	// Now search for the original image. It should have been stored in the database
	// as part of creating the small one.
	assertAlbumImage(t, lib, 1, OriginalImage, bigImage)

	// Search for an image for album which is not in the database at all.
	_, err = lib.FindAndSaveAlbumArtwork(ctx, 42, OriginalImage)
	if !errors.Is(err, ErrAlbumNotFound) {
		t.Errorf("expected error `%+v` but got `%+v`", ErrAlbumNotFound, err)
	}

	// Now, create a new album and store an image for it. Then try to get it from the
	// library right away.
	secondFile := MockMedia{
		artist: "Unit Runner",
		album:  "The Test Strikes Back",
		title:  "Good Coverage",
		track:  2,
		length: 621,
	}
	if err := lib.insertMediaIntoDatabase(&secondFile, secondFilePath); err != nil {
		t.Fatalf("inserting second media file failed: %s", err)
	}

	err = lib.SaveAlbumArtwork(ctx, 2, bytes.NewReader(secondBigImage))
	if err != nil {
		t.Fatalf("error saving an album image: %s", err)
	}
	assertAlbumImage(t, lib, 2, OriginalImage, secondBigImage)

	// Now get the small version of this original image. This tests converting
	// a big original in the database into the desired size when this size was
	// not found.
	assertAlbumImage(t, lib, 2, SmallImage, smallImage)

	// Try finding an image on the file system. Making sure to create a new album
	// before that.
	thirdFile := MockMedia{
		artist: "Unit Runner",
		album:  "Into The New Regressions We Go",
		title:  "Forever More",
		track:  3,
		length: 112,
	}
	if err := lib.insertMediaIntoDatabase(&thirdFile, thirdFilePath); err != nil {
		t.Fatalf("inserting third media file failed: %s", err)
	}
	assertAlbumImage(t, lib, 3, OriginalImage, []byte(thirdAlbumCover))

	// And now, remove an album's image from the database and make sure it is
	// deleted.
	if err = lib.RemoveAlbumArtwork(ctx, 2); err != nil {
		t.Fatalf("error removing artist image: %s", err)
	}

	_, err = lib.FindAndSaveAlbumArtwork(ctx, 2, OriginalImage)
	if !errors.Is(err, ErrArtworkNotFound) {
		t.Fatalf("expected artwork not found error but got `%+v`", err)
	}
}

func assertAlbumImage(
	t *testing.T,
	lib *LocalLibrary,
	albumID int64,
	size ImageSize,
	expectedImage []byte,
) {
	ctx := context.Background()

	foundImg, err := lib.FindAndSaveAlbumArtwork(ctx, albumID, size)
	if err != nil {
		t.Fatalf("error finding album image: %s", err)
	}

	foundImgBytes, err := ioutil.ReadAll(foundImg)
	if err != nil {
		t.Fatalf("error reading image reader: %s", err)
	}

	if !bytes.Equal(expectedImage, foundImgBytes) {
		t.Errorf("expected image `%s` but got `%s`", expectedImage, foundImgBytes)
	}
}