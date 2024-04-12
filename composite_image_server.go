package main

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"os"

	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	"strconv"

	"encoding/json"
	"math"
	"math/rand"

	"github.com/disintegration/imaging"
	"github.com/nfnt/resize"

	"path/filepath"
	"strings"
)

var template_set *template.Template

var live_count = 0

func Abs(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

type Overlap struct {
	overlaps      bool // Will just be true if both x & y overlaps are true
	overlaps_in_x bool
	overlaps_in_y bool
	x_to_left     int32 // How far the other would have to move to the left to no longer overlap (-ve means clearance)
	x_to_right    int32 // How far the other would have to move to the right to no longer overlap (-ve means clearance)
	y_to_top      int32
	y_to_bottom   int32
}

func (overl *Overlap) as_str() string {
	res := fmt.Sprintf("Overlapping: %t, X_overlap: %t, Y_overlap: %t, x_to_left: %d, x_to_right: %d, y_to_top: %d, y_to_bottom: %d\n",
		overl.overlaps,
		overl.overlaps_in_x,
		overl.overlaps_in_y,
		overl.x_to_left,
		overl.x_to_right,
		overl.y_to_top,
		overl.y_to_bottom)
	return res

}

type CoG struct {
	x    int
	y    int
	mass int
}

// Data structure to represent the set of previously fetched image

type SnapshotSet struct {
	Snaps []*Snapshot `json:"snaps"`
}

func (snapset *SnapshotSet) append(snap Snapshot) {
	snapset.Snaps = append(snapset.Snaps, &snap)

}

func (snapset *SnapshotSet) get_CoG() CoG {
	var mass_ac_x int
	var mass_ac_y int
	var total_mass int
	for _, snap := range snapset.Snaps {
		cog := snap.get_CoG()
		mass_ac_x += cog.mass * cog.x
		mass_ac_y += cog.mass * cog.y
		total_mass += cog.mass
	}
	if total_mass == 0 {
		log.Fatalf("Couldn't find the CoG of %v\n", snapset)
	}
	x := mass_ac_x / total_mass
	y := mass_ac_y / total_mass

	return CoG{x: x, y: y, mass: total_mass}

}

func (snapset *SnapshotSet) overlaps(snap *Snapshot) bool {
	// Tests the snap against every one in the set
	// returns true if it overlaps
	for _, original := range snapset.Snaps {
		overlaps := find_overlap(original, snap)
		if overlaps.overlaps {
			return true
		}
	}
	return false
}

func (snapset *SnapshotSet) possible_positions(snap *Snapshot) []Pair {
	// returns all the possible nodes for a new photo to be added
	all_lines := make([]Pair, 0, len(snapset.Snaps)*3+1)
	set_cog := snapset.get_CoG()
	// Add CoG as a centre...
	cen_x := int32(set_cog.x) - snap.Width/2
	cen_y := int32(set_cog.y) - snap.Height/2
	all_lines = append(all_lines, Pair{cen_x, cen_y})

	// Now from all the vertices...
	for _, original := range snapset.Snaps {
		positions := original.get_positions(snap)
		all_lines = append(all_lines, positions...)

	}
	// We now need to perm all the x's against all the y's to get all the positions!
	all_positions := make([]Pair, 0, len(snapset.Snaps)*len(snapset.Snaps)*9)
	for _, outer_point := range all_lines {
		for _, inner_point := range all_lines {

			all_positions = append(all_positions, Pair{outer_point.lower, inner_point.upper})

		}
	}

	return all_positions
}

func (snapset *SnapshotSet) find_best_position(newsnap *Snapshot) Pair {
	cog := snapset.get_CoG()
	closest_distance := 1000000000
	positions := snapset.possible_positions(newsnap)
	best_found_position := Pair{0, 0}

	for _, position := range positions {
		newsnap.X = position.lower
		newsnap.Y = position.upper

		if !snapset.overlaps(newsnap) {
			// It doesn't overlap, so how close is it to the CoG
			newone_cog := newsnap.get_CoG()
			dx := cog.x - newone_cog.x
			dy := cog.y - newone_cog.y
			step_sqr := dx*dx + 2*dy*dy
			if step_sqr < closest_distance {
				closest_distance = step_sqr
				best_found_position = position
			}

		}
	}
	return best_found_position

}

func (snapset *SnapshotSet) fit_another(newsnap *Snapshot) {
	position := snapset.find_best_position(newsnap)
	newsnap.X = position.lower
	newsnap.Y = position.upper
	snapset.append(*newsnap)
}

func (snapset *SnapshotSet) rescale_to_window(width int, height int) {
	// Once a set has been fitted
	// This will adjust the set so that the result fits inside the width and height
	// and is generally centred
	min_x := int32(1000000)
	max_x := int32(-1000000)
	min_y := int32(1000000)
	max_y := int32(-1000000)

	for _, snap := range snapset.Snaps {
		if snap.X < min_x {
			min_x = snap.X
		}
		if snap.X+snap.Width > max_x {
			max_x = snap.X + snap.Width
		}
		if snap.Y < min_y {
			min_y = snap.Y
		}
		if snap.Y+snap.Height > max_y {
			max_y = snap.Y + snap.Height
		}
	}
	gain_to_fit_x := float32(width) / float32(max_x-min_x)
	gain_to_fit_y := float32(height) / float32(max_y-min_y)
	var gain float32
	var x_leftover_end_units int
	var y_leftover_end_units int
	if gain_to_fit_x > gain_to_fit_y {
		gain = gain_to_fit_y
		x_leftover_end_units = (width - int(float32(max_x-min_x)*gain)) / 2
		y_leftover_end_units = 0
	} else {
		gain = gain_to_fit_x
		x_leftover_end_units = 0
		y_leftover_end_units = (height - int(float32(max_y-min_y)*gain)) / 2
	}

	for _, snap := range snapset.Snaps {

		snap.X = int32(math.Ceil(float64(snap.X-min_x)*float64(gain))) + int32(x_leftover_end_units)
		snap.Y = int32(math.Ceil(float64(snap.Y-min_y)*float64(gain))) + int32(y_leftover_end_units)
		snap.Width = int32(math.Floor(float64(gain) * float64(snap.Width)))
		snap.Height = int32(math.Floor(float64(gain) * float64(snap.Height)))

	}

}

// Core data structure for a single photo

type Snapshot struct {
	Width    int32  `json:"width"`
	Height   int32  `json:"height"`
	X        int32  `json:"x"`
	Y        int32  `json:"y"`
	Location string `json:"location"`
}

func (snap *Snapshot) get_positions(other *Snapshot) []Pair {
	// Return three points, to get all the relevant x&y positions we might want
	positions := make([]Pair, 3)

	leftmost_x := snap.X - other.Width
	topmost_y := snap.Y - other.Height
	positions[0] = Pair{leftmost_x, topmost_y}

	centre_x := snap.X + snap.Width/2 - other.Width/2
	centre_y := snap.Y + snap.Height/2 - other.Height/2

	positions[1] = Pair{centre_x, centre_y}

	rightmost_x := snap.X + snap.Width
	bottommost_y := snap.Y + snap.Height

	positions[2] = Pair{rightmost_x, bottommost_y}

	return positions
}
func (snap *Snapshot) draw_rect(canvas *image.RGBA, col NamedColour) {
	// top line
	x := int(snap.X)
	max_x := int(snap.X + snap.Width)
	y := int(snap.Y)
	max_y := int(snap.Y + snap.Height)
	for ; x < max_x; x++ {
		canvas.Set(x, y, col.get_Colour())
	}
	for ; y < max_y; y++ {
		canvas.Set(x, y, col.get_Colour())
	}
	for ; x > int(snap.X); x-- {
		canvas.Set(x, y, col.get_Colour())
	}
	for ; y > int(snap.Y); y-- {
		canvas.Set(x, y, col.get_Colour())
	}
}
func (snap *Snapshot) draw(img *image.RGBA, col NamedColour) {
	draw.Draw(img,
		snap.getRect(),
		&image.Uniform{col.get_Colour()},
		image.ZP,
		draw.Over)

}

func (snap *Snapshot) get_CoG() CoG {
	x := snap.X + snap.Width/2
	y := snap.Y + snap.Height/2
	mass := snap.Width * snap.Height
	res := CoG{x: int(x), y: int(y), mass: int(mass)}
	return res
}

func (snap *Snapshot) getRect() image.Rectangle {
	rect := image.Rect(int(snap.X),
		int(snap.Y),
		int(snap.X+snap.Width),
		int(snap.Y+snap.Height))

	return rect

}

func (snap *Snapshot) get_str() string {
	res := fmt.Sprintf("Snap: (%d,%d)->(%d,%d)", snap.X, snap.X, snap.X+snap.Width, snap.Y+snap.Height)
	return res
}

type Pair struct {
	lower int32
	upper int32
}

type Cover struct {
	// returned value for 1d comparison
	// where lowside_offset is how far
	overlaps        bool  // whether it overlaps at all
	lowside_offset  int32 // how much smaller the position of the second would have to be to clear
	highside_offset int32 // how much larger the position of the second would have to be to clear
}

func find_cover(first *Pair, second *Pair) Cover {
	// Takes 2d Pairs and calculates their

	// how much greater is the right side of the second than the left side of the first
	lowside := second.upper - first.lower

	// how much lower is the left side of the second than the right side of the first
	highside := first.upper - second.lower

	// If either number is negative then we have clearance

	overlaps := (lowside > 0 && highside > 0)

	//fmt.Printf("1st:(%d,%d), 2nd:(%d,%d) -> lowside:%d,highside:%d -> overlap:%t\n", first.lower, first.upper, second.lower, second.upper, lowside, highside, overlaps)

	return Cover{overlaps, lowside, highside}

}

func find_overlap(first *Snapshot, second *Snapshot) Overlap {
	// Checks for overlap in both x & y dimensions
	first_xes := Pair{first.X, first.X + first.Width}
	second_xes := Pair{second.X, second.X + second.Width}
	x_cover := find_cover(&first_xes, &second_xes)
	first_yes := Pair{first.Y, first.Y + first.Height}
	second_yes := Pair{second.Y, second.Y + second.Height}
	y_cover := find_cover(&first_yes, &second_yes)
	return Overlap{x_cover.overlaps && y_cover.overlaps,
		x_cover.overlaps,
		y_cover.overlaps,
		x_cover.lowside_offset,
		x_cover.highside_offset,
		y_cover.lowside_offset,
		y_cover.highside_offset}
}

type Page struct {
	Title string

	Body []byte
}

func (p *Page) save() error {

	filename := p.Title + ".txt"

	return os.WriteFile(filename, p.Body, 0600)

}

func loadPage(title string) (*Page, error) {

	filename := title + ".txt"

	path, err := os.Getwd()
	if err != nil {
		fmt.Println(err)
	}
	fmt.Printf("Current working directory: %s\n", path)

	fmt.Printf("About to load: %s\n", filename)

	whole_name_and_path := path + `\` + filename

	//whole_name_and_path = `C:\Users\edwar\Insync\edwardmfrench@gmail.com\Google Drive\Pinpoint_Capital\Project Ugla\trygo\TestPage.txt`

	fmt.Printf("Whole name and path: %s \n", whole_name_and_path)

	body, err := os.ReadFile(whole_name_and_path)

	if err != nil {

		fmt.Printf("Error is : %v \n", err)

		return nil, err

	} else {
		fmt.Printf("Found the file on disc with size of : %d\n", len(body))
	}

	return &Page{Title: title, Body: body}, nil

}

func renderTemplate(response http.ResponseWriter, template_name string, page *Page) {
	template_filename := template_name + ".html"
	fmt.Printf("Template filename : %s\n", template_filename)

	contains_this_template := template_set.Lookup(template_filename)
	fmt.Printf("Lookup on the template_set result: %v \n", contains_this_template)

	template, err := template_set.ParseFiles(template_filename)
	if err != nil {
		fmt.Printf("**** Failed to find the template: \"%s\"\n\t In template_set %v \n\tWith DefinedTemplates: %s\n\t Error: %v\n", template_filename, template_set, template_set.DefinedTemplates(), err)
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Printf("Template set up : %v\n", template)
	err = template.Execute(response, page)
	if err != nil {
		fmt.Printf("Failed to render the template: %v \n", err)
		http.Error(response, err.Error(), http.StatusInternalServerError)
	}
}

func make_random_snapshot() *Snapshot {
	width := 60 + rand.Int31n(100)
	height := 60 + rand.Int31n(100)
	x := 250 + rand.Int31n(500-width)
	y := 250 + rand.Int31n(500-height)
	return &Snapshot{width, height, x, y, "random"}
}

func draw_CoG(img *image.RGBA, cog CoG) {
	draw_crosshairs(img, int32(cog.x), int32(cog.y))
}

func draw_crosshairs(img *image.RGBA, x int32, y int32, half_size_l ...int32) {
	// Draws a cross hair
	var half_size int32
	if len(half_size_l) == 0 {
		half_size = 20
	} else {
		half_size = half_size_l[0]
	}
	yellow := color.RGBA{255, 255, 255, 99}

	vertrect := image.Rect(int(x),
		int(y-half_size),
		int(x+1),
		int(y+half_size))

	draw.Draw(img,
		vertrect,
		&image.Uniform{yellow},
		image.ZP,
		draw.Src)

	horizrect := image.Rect(int(x-half_size),
		int(y),
		int(x+half_size),
		int(y+1))

	draw.Draw(img,
		horizrect,
		&image.Uniform{yellow},
		image.ZP,
		draw.Src)

}

type NamedColour struct {
	name string
	R    uint8
	G    uint8
	B    uint8
	A    uint8
}

func (nc NamedColour) get_Colour() color.RGBA {
	return color.RGBA{nc.R, nc.G, nc.B, nc.A}
}

var (
	SOLID_BLUE         NamedColour = NamedColour{"SOLID BLUE", 10, 10, 175, 255}
	GHOSTLY_GREY_GREEN NamedColour = NamedColour{"GHOSTLY GREY GREEN", 30, 255, 50, 10}
	CHUNKY_GREEN       NamedColour = NamedColour{"CHUNKY GREEN", 0, 200, 0, 100}
)

func test_layout_handler(w http.ResponseWriter, r *http.Request) {

	image_filenames, err := fetch_local_image_filenames("photos/")
	if err != nil {
		log.Fatal("Couldn't fetch the image filenames")
		http.Error(w, "Couldn't fetch the image filenames", 500)
		return
	}

	snapshots, err := snapshots_from_local_filenames(image_filenames, "photos/")
	if err != nil {
		log.Fatal("Couldn't fetch the snapshots")
		http.Error(w, "Couldn't fetch the snapshots", 500)
		return
	}

	log.Print(snapshots)

	// Canvas
	canvas := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	grey := color.RGBA{10, 10, 10, 255}
	draw.Draw(canvas,
		canvas.Bounds(),
		&image.Uniform{grey},
		image.Point{},
		draw.Src)

	snap_set := SnapshotSet{}

	// Initial rectangle

	//first_img := make_random_snapshot()
	// first_img := &Snapshot{100, 100, 450, 450, "centre"}
	// snap_set.append(*first_img)
	// fmt.Printf("First: %s\n", first_img.get_str())
	// first_img.draw(canvas, SOLID_BLUE)
	// draw_CoG(canvas, first_img.get_CoG())

	// Add the first image as a seed
	first_img := snapshots[0]
	first_img.X = 5000 - first_img.Width/2
	first_img.Y = 5000 - first_img.Height/2
	snap_set.append(first_img)
	//first_img.draw(canvas, SOLID_BLUE)

	// Now add some more....

	for i := 1; i < len(snapshots); i++ {
		//cog := snap_set.get_CoG()
		// log.Printf("Cog: %v\n", cog)
		draw_CoG(canvas, snap_set.get_CoG())

		another_img := snapshots[i]
		snap_set.fit_another(&another_img)
		//another_img.draw_rect(canvas, GHOSTLY_GREY_GREEN)

	}

	snap_set.rescale_to_window(1200, 800)

	for _, snap := range snap_set.Snaps {
		snap.draw_rect(canvas, GHOSTLY_GREY_GREEN)
	}

	var img image.Image = canvas
	writeImage(w, &img)
}

// writeImage encodes an image 'img' in jpeg format and writes it into ResponseWriter.
func writeImage(w http.ResponseWriter, img *image.Image) {

	buffer := new(bytes.Buffer)
	if err := jpeg.Encode(buffer, *img, nil); err != nil {
		log.Println("unable to encode image.")
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(buffer.Bytes())))
	if _, err := w.Write(buffer.Bytes()); err != nil {
		log.Println("unable to write image.")
	}
}

func editHandler(w http.ResponseWriter, r *http.Request) {

	title := r.URL.Path[len("/edit/"):]

	p, err := loadPage(title)

	if err != nil {

		fmt.Printf("Making a new page for : %s", title)
		p = &Page{Title: title}

	}

	renderTemplate(w, "edit", p)

}

func compositeMapHandler(response http.ResponseWriter, request *http.Request) {
	// Return map of the images required as json
	height, err := strconv.Atoi(request.URL.Query().Get("height"))
	if err != nil {
		http.Error(response, "Missing height parameter", 400)
		return
	}
	width, err := strconv.Atoi(request.URL.Query().Get("width"))
	if err != nil {
		http.Error(response, "missing width parameter", 400)
		return
	}

	log.Printf("Found in request for composite data:\n\tHeight: %v\n\tWidth: %v\n", height, width)

	// Fetch the list of images, and setup list of snapshots to be fitted

	image_filenames, err := fetch_local_image_filenames("photos/")
	if err != nil {
		log.Fatal("Couldn't fetch the image filenames")
		http.Error(response, "Couldn't fetch the image filenames", 500)
		return
	}

	snapshots, err := snapshots_from_local_filenames(image_filenames, "photos/")
	if err != nil {
		log.Fatal("Couldn't fetch the snapshots")
		http.Error(response, "Couldn't fetch the snapshots", 500)
		return
	}

	log.Print(snapshots)

	// Create the snapshotset with all of the images placed
	// On to a theoretical huge canvas 5000x5000

	snap_set := SnapshotSet{}

	//Place the first in the middle of that field
	first_img := snapshots[0]
	first_img.X = 5000 - first_img.Width/2
	first_img.Y = 5000 - first_img.Height/2
	snap_set.append(first_img)

	// Now add some more to minimise gravity

	for i := 1; i < len(snapshots); i++ {
		//cog := snap_set.get_CoG()
		//log.Printf("Cog: %v\n", cog)

		another_img := snapshots[i]
		snap_set.fit_another(&another_img)

	}

	// rescale to the window
	snap_set.rescale_to_window(width, height)

	// make the json

	jsonset, err := json.Marshal(snap_set)

	if err != nil {
		log.Fatalf("Failed to jsonise:\n\t%v\n\t%v\n", snap_set, err)
	}
	http.ResponseWriter.Header(response).Set("Content-Type", "application/json")
	response.Write(jsonset)

}

func compositePageHandler(response http.ResponseWriter, request *http.Request) {

	varmap := map[string]interface{}{
		"table": "none",
		"spare": "other",
	}

	// duration := 10 * time.Second

	// time.Sleep(duration)

	err := template_set.ExecuteTemplate(response, "composite.html", varmap)
	if err != nil {
		log.Fatalf("Failed to execute template composite.html %v", err)
	}

}

func homeHandler(response http.ResponseWriter, request *http.Request) {
	response.Write([]byte(`<a href="/composite_page">Composite Page</a>`))

}

func fetch_local_image_filenames(image_path string) ([]string, error) {

	all_image_filenames, err := filepath.Glob(image_path + "*.[jJ][pP][gG]")
	if err != nil {
		return nil, err
	}

	// We only want the filename from the whole thing
	for i, filename := range all_image_filenames {
		all_image_filenames[i] = filepath.Base(filename)

	}
	log.Printf("Found %v images to consider\nFirst is : %v\n", len(all_image_filenames), all_image_filenames[0])

	// choose quantity

	image_count := rand.Intn(30) + 1
	if image_count > 12 {
		image_count = 1
	}

	image_set := make([]string, 0, image_count)
	image_indexes := make(map[int]bool)

	for {
		// Are we done
		if len(image_set) == image_count {
			break
		}
		// Try and add one
		index := rand.Intn(len(all_image_filenames))
		if !image_indexes[index] {
			image_indexes[index] = true
			new_image := all_image_filenames[index]
			image_set = append(image_set, new_image)
		}

	}
	log.Printf("Found image set filenames: %v\n", image_set)
	return image_set, nil
}

func fetch_image_from_file(filepath string, filename string) (image.Image, error) {
	img, err := imaging.Open(filepath+filename, imaging.AutoOrientation(true))

	return img, err
}

func fetch_and_resize_image_from_file(filepath string, filename string, width int) (image.Image, error) {
	img, err := fetch_image_from_file(filepath, filename)

	if err != nil {
		return nil, err
	}
	resized := resize.Resize(uint(width), 0, img, resize.Bilinear)
	return resized, nil

}

func photoHandler(response http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	log.Printf("Path: %v\n", path)
	filename, _ := strings.CutPrefix(path, "/photograph/")

	width, err := strconv.Atoi(request.URL.Query().Get("width"))
	if err != nil {
		http.Error(response, "Missing width parameter", 400)
		return
	}
	if width > 4000 || width < 10 {
		http.Error(response, "Width must be between 10 and 4000", 400)
		return
	}

	img, err := fetch_and_resize_image_from_file("photos/", filename, width)
	if err != nil {
		http.Error(response, err.Error(), 500)
		return
	}
	response.Header().Set("Content-Type", "image/jpeg")
	jpeg.Encode(response, img, nil)

}

func snapshot_from_jpeg_file(filename string, filepath string) (Snapshot, error) {
	// file, err := os.Open(filepath + filename)

	// if err != nil {
	// 	log.Printf("Failed to load : %v\nErr:%v\n", filename, err)
	// 	return Snapshot{}, err
	// }
	// defer file.Close()

	// img, err := jpeg.Decode(file)

	img, err := fetch_image_from_file(filepath, filename)

	if err != nil {
		log.Printf("Failed to decode the image: %v\n", filename)
		return Snapshot{}, err
	}
	res := Snapshot{
		X:        0,
		Y:        0,
		Width:    int32(img.Bounds().Dx()),
		Height:   int32(img.Bounds().Dy()),
		Location: filename,
	}
	return res, nil // comment

}

func snapshots_from_local_filenames(filenames []string, filepath string) ([]Snapshot, error) {
	snaps := make([]Snapshot, 0, len(filenames))
	for _, filename := range filenames {
		snap, err := snapshot_from_jpeg_file(filename, filepath)

		if err != nil {
			log.Printf("IGNORING BAD FILE: %v\n", filename)

		} else {
			snaps = append(snaps, snap)
		}
	}
	return snaps, nil
}

// var random *rand.Rand

func Check_all_images(path string) (goods []string, bads []string) {
	filenames, err := filepath.Glob(path + "*.jpg")
	if err != nil {
		log.Fatal(err)
	}
	for i, filename := range filenames {
		filenames[i] = filepath.Base(filename)

	}

	if err != nil {
		log.Fatal(err)
	}
	for _, filename := range filenames {
		_, err := fetch_image_from_file(path, filename)
		if err != nil {
			bads = append(bads, filename)
		} else {
			goods = append(goods, filename)
		}
	}
	return goods, bads
}

func report_bad_images(path string) {
	goods, bads := Check_all_images(path)
	fmt.Printf("Checked %v files\n", len(goods)+len(bads))
	fmt.Printf("Bad files: %v\n", bads)

}

func main() {

	// random = rand.New(rand.NewSource(99))

	//report_bad_images("photos/")

	log.Println("STARTED")

	path, err := os.Getwd()
	if err != nil {
		log.Println(err)
	}
	fmt.Printf("Initial working directory: %v\n", path)

	//os.Chdir("/")

	path, err = os.Getwd()
	if err != nil {
		log.Println(err)
	}
	fmt.Printf("Working directory: %v\n", path)

	c, err := os.ReadDir(".")
	if err != nil {

		log.Fatal(err)
	}
	fmt.Println(c)

	template_set, err = template.ParseGlob("./templates/*")
	if err != nil {
		log.Println("Cannot parse templates:", err)
		os.Exit(-1)
	}

	fmt.Printf("Template set loaded: %s \n", template_set.DefinedTemplates())

	// template_filename := "view" + ".html"
	// fmt.Printf("Template filename : %s\n", template_filename)

	// contains_this_template := template_set.Lookup(template_filename)
	// fmt.Printf("Lookup on the template_set result: %v \n", contains_this_template)

	// template, err := template_set.ParseFiles(template_filename)
	// if err != nil {
	// 	fmt.Printf("**** Failed to find the template: \"%s\"\n\t In template_set %v \n\tWith DefinedTemplates: %s\n\t Error: %v\n", template_filename, template_set, template_set.DefinedTemplates(), err)
	// 	return
	// }
	// fmt.Printf("Template set up : %v\n", template)
	// var template_buffer bytes.Buffer
	// // if err := template.Execute(&template_buffer, nil); err != nil {
	// // 	return
	// // }

	// result := template_buffer.String()
	// fmt.Printf("Result of processing template: %v", result)

	// if err != nil {
	// 	fmt.Printf("Failed to render the template: %v \n", err)
	// 	return
	// }

	fs := http.FileServer(http.Dir("./photos"))
	http.Handle("/photos/", http.StripPrefix("/photos/", fs)) // stripPrefix("/photos/", fs") fs)

	http.HandleFunc("/test_layout_handler/", test_layout_handler)

	http.HandleFunc("/composite_map/", compositeMapHandler)

	http.HandleFunc("/composite_page/", compositePageHandler)

	http.HandleFunc("/photograph/", photoHandler)

	http.HandleFunc("/", homeHandler)

	fmt.Println("Starting server on port 8080")

	log.Fatal(http.ListenAndServe(":8080", nil))

}
