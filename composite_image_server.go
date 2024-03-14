package main

import (
	"bufio"
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"time"

	"os"

	"strings"

	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	"strconv"

	"encoding/json"
	"math"
	"math/rand"
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

type Direction struct {
	xd   int32
	yd   int32
	name string
}

var (
	LEFTWARDS  = Direction{-1, 0, "leftwards"}
	RIGHTWARDS = Direction{1, 0, "rightwards"}
	DOWNWARDS  = Direction{0, 1, "downwards"}
	UPWARDS    = Direction{0, -1, "upwards"}
)

type PlacementSet []struct {
	ImageCount int `json:"image_count"`
	Placements []struct {
		SizeIndex       int   `json:"size_index"`
		PlacementOrder  int   `json:"placement_order"`
		OffsetDirection []int `json:"offset_direction"`
	} `json:"placements"`
}

// type Canvas struct {
// 	// Unbounded notional canvas for assembling photos
// 	photos []Snapshot // Will contain all the photos added so far
// }

// Layouts
// 1 photo:

// Size ordering, not the same as placement order!

// 	1

// --------------
// 	1  or 1	2
// 	2
// --------------
// 	  1
// 	2   3
// ---------------
// 	1   4

// 	3   2
// ----------------

//       2   1
// 	4   3   5

// -----------------
//      3   2   5
//      6	 1   4

// -----------------

//        2    4
//       6   1   7
//        5     3
// -------------------

//         2     3
//         5   1   4
//       7    6    8

// --------------------

//       4      3      7
//       8      1     9
//       5      2      6

// ---------------------
// Acceptable quantites
// 1, 2, 4,

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

func viewHandler(response http.ResponseWriter, request *http.Request) {
	title := request.URL.Path[len("/view/"):]
	fmt.Printf("Request to view page : %s", title)
	page, err := loadPage(title)
	if err != nil {
		fmt.Println("Failed to load the page from disc")
		http.Redirect(response, request, "/edit/"+title, http.StatusFound)
		return
	}
	fmt.Println("About to apply the template....")
	renderTemplate(response, "view", page)
}

func saveHandler(response http.ResponseWriter, request *http.Request) {
	title := request.URL.Path[len("/save/"):]
	body := request.FormValue("body")
	page := &Page{Title: title, Body: []byte(body)}
	err := page.save()
	if err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(response, request, "/view/"+title, http.StatusFound)
}

func make_random_snapshot() *Snapshot {
	width := 60 + random.Int31n(100)
	height := 60 + random.Int31n(100)
	x := 250 + random.Int31n(500-width)
	y := 250 + random.Int31n(500-height)
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

func blueHandler(w http.ResponseWriter, r *http.Request) {

	image_filenames := fetch_image_filenames()

	snapshots, err := snapshots_from_filenames(image_filenames)
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
		cog := snap_set.get_CoG()
		log.Printf("Cog: %v\n", cog)
		draw_CoG(canvas, snap_set.get_CoG())

		another_img := snapshots[i]
		snap_set.fit_another(&another_img)
		//another_img.draw_rect(canvas, GHOSTLY_GREY_GREEN)

	}

	snap_set.rescale_to_window(1200, 800)

	for _, snap := range snap_set.Snaps {
		snap.draw_rect(canvas, GHOSTLY_GREY_GREEN)
	}

	// snap_set.append(*another_img)
	// another_img.draw_rect(canvas, SOLID_BLUE)

	// second_img := make_random_snapshot()

	// positions := snap_set.possible_positions(second_img)

	// for _, position := range positions {
	// 	second_img.tl_x = position.lower
	// 	second_img.tl_y = position.upper
	// 	second_img.draw_rect(canvas, GHOSTLY_GREY_GREEN)
	// }

	// fmt.Printf("Second: %s\n", second_img.get_str())
	// second_img.draw(canvas, GHOSTLY_GREY_GREEN)
	// draw_CoG(canvas, second_img.get_CoG())

	// overlapis := find_overlap(first_img, second_img)

	// fmt.Println(overlapis.as_str())

	// // Figure out shortest move to clear in X & Y

	// var x_move_to_clear int32 = 0
	// var y_move_to_clear int32 = 0

	// if overlapis.x_to_left < overlapis.x_to_right {
	// 	x_move_to_clear = -overlapis.x_to_left
	// } else {
	// 	x_move_to_clear = overlapis.x_to_right
	// }
	// if overlapis.y_to_top < overlapis.y_to_bottom {
	// 	y_move_to_clear = -overlapis.y_to_top
	// } else {
	// 	y_move_to_clear = overlapis.y_to_bottom
	// }

	// // set a boolean for if we're going to clear by moving in x (false=y)
	// clear_in_x := (Abs(x_move_to_clear) < Abs(y_move_to_clear))

	// moved_second := second_img

	// if clear_in_x {
	// 	moved_second.tl_x = moved_second.tl_x + x_move_to_clear
	// } else {
	// 	moved_second.tl_y = moved_second.tl_y + y_move_to_clear
	// }
	// fmt.Printf("Moved 2nd: %s\n", moved_second.get_str())
	// moved_second.draw(canvas, CHUNKY_GREEN)
	// draw_CoG(canvas, moved_second.get_CoG())

	// snap_set.append(*moved_second)

	// // Draw new CoG
	// draw_CoG(canvas, snap_set.get_CoG())

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

	image_filenames := fetch_image_filenames()

	snapshots, err := snapshots_from_filenames(image_filenames)
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
		cog := snap_set.get_CoG()
		log.Printf("Cog: %v\n", cog)

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
	// Return html template for the page

	// template, err := template_set.ParseFiles("composite.html")
	// if err != nil {
	// 	log.Printf("Error loading template: \n%v\n", err)
	// 	return
	// }

	// err = template.Execute(response, "")

	// if err != nil {
	// 	log.Printf("Error rendering template:\n%v\n", err)
	// }

	varmap := map[string]interface{}{
		"table": "none",
		"spare": "other",
	}

	// duration := 10 * time.Second

	// time.Sleep(duration)

	err := template_set.ExecuteTemplate(response, "composite.html", varmap)
	if err != nil {
		log.Fatalf("Failed to execute template home.html %v", err)
	}

}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	live_count++
	files, err := os.ReadDir("./")
	if err != nil {
		log.Fatal(err)
	}
	var builder strings.Builder

	for _, filename := range files {
		name := filename.Name()
		if strings.HasSuffix(name, ".txt") {
			name = strings.TrimSuffix(name, ".txt")
			builder.WriteString(`<li><a href="`)
			builder.WriteString("/view/")
			builder.WriteString(name)
			builder.WriteString(`">`)
			builder.WriteString(name)
			builder.WriteString("</a></li>\n")
		}

	}
	// live_count_html := fmt.Sprintf("<h1>Live count %d</h1>", live_count)
	// builder.WriteString(live_count_html)

	// tpl := template.Must(template.New("main").Parse(`{{define "T"}}{{.table}}{{.spare}}{{end}}`))

	varmap := map[string]interface{}{
		"table": template.HTML(builder.String()),
		"spare": "other",
	}

	// duration := 10 * time.Second

	// time.Sleep(duration)

	err = template_set.ExecuteTemplate(w, "home.html", varmap)
	if err != nil {
		log.Fatalf("Failed to execute template home.html %v", err)
	}
	// live_count--

}

func fetch_image_filenames() []string {

	image_size_selector := random.Intn(20) + 1

	if image_size_selector > 9 {
		image_size_selector = 1
	}
	max_count := image_size_selector
	min_count := image_size_selector

	IMAGE_SET_URL := fmt.Sprintf("http://192.168.1.125/photo_list?max_count=%v&min_count=%v", max_count, min_count)

	client := http.Client{
		Timeout: time.Second * 15, // Timeout after 2 seconds
	}

	req, err := http.NewRequest(http.MethodGet, IMAGE_SET_URL, nil)
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Set("User-Agent", "go_image_tiler")

	res, getErr := client.Do(req)
	if getErr != nil {
		log.Fatal(getErr)
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		log.Fatal(readErr)
	}

	log.Println(string(body))

	var photo_files []string

	json.Unmarshal(body, &photo_files)

	log.Println(photo_files)

	return photo_files
}

func check_fetchable_image(filename string) bool {
	client := http.Client{
		Timeout: 60 * time.Second,
	}

	res, err := client.Get("http://192.168.1.125/fetch_photo/" + filename)

	if err != nil || res.StatusCode != 200 {
		log.Printf("Failed to fetch image: %v\n", filename)
		return false
	}
	defer res.Body.Close()

	image, _, err := image.Decode(res.Body)
	if image.Bounds().String() == "fishcakes" {
		fmt.Println("fishcakes")
	}
	if err != nil {
		log.Printf("Couldn't decode the image:\n\t\t%v\n\t\t%v\n\t\tSize of body:%v\n", filename, err, res.ContentLength)
		return false
	}
	return true

}

func snapshots_from_filenames(filenames []string) ([]Snapshot, error) {
	snaps := make([]Snapshot, len(filenames))
	client := http.Client{
		Timeout: 60 * time.Second,
	}

	for i, filename := range filenames {

		res, err := client.Get("http://192.168.1.125/measure_photo_size/" + filename)

		if err != nil || res.StatusCode != 200 {
			// handle errors
			log.Printf("Couldn't fetch the image size:%v\n", filename)
			return nil, err
		}
		defer res.Body.Close()

		// extract the size (it'll be in a string that looks like (width,height))
		body_bytes, err := io.ReadAll(res.Body)
		if err != nil {
			log.Fatalln(err)
		}

		bits := strings.Split(string(body_bytes), ",")
		width, err := strconv.Atoi(bits[0][1:])
		if err != nil {
			log.Fatalln(err)
		}

		height, err := strconv.Atoi(bits[1][1 : len(bits[1])-1])
		if err != nil {
			log.Fatalln(err)
		}

		// Create a new snapshot
		snaps[i] = Snapshot{int32(width), int32(height), 0, 0, filename}

	}
	return snaps, nil

}

var random *rand.Rand

func check_all_images() error {
	// Open the file for reading
	readFile, err := os.Open("image_list.txt")

	if err != nil {
		fmt.Println(err)
	}
	fileScanner := bufio.NewScanner(readFile)
	fileScanner.Split(bufio.ScanLines)
	var fileLines []string

	for fileScanner.Scan() {
		fileLines = append(fileLines, fileScanner.Text())
	}

	readFile.Close()

	// for _, line := range fileLines {
	// 	fmt.Println(line)
	// }

	// fmt.Println(fileLines)

	bads := make([]string, 100)

	for _, filename := range fileLines {
		works := check_fetchable_image(filename)
		if !works {
			bads = append(bads, filename)
			fmt.Printf("BAD=%v\n", filename)
		}
	}
	return nil
}

func main() {

	random = rand.New(rand.NewSource(99))

	//check_all_images()

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

	dat, err := os.ReadFile("layouts.json")
	if err != nil {

		log.Fatal(err)
	}

	var placementset PlacementSet
	if err := json.Unmarshal(dat, &placementset); err != nil {
		log.Fatal(err)
	}

	log.Printf("placementset[1].Placements[0].OffsetDirection: %v", placementset[1].Placements[0].OffsetDirection)

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

	http.HandleFunc("/view/", viewHandler)

	http.HandleFunc("/edit/", editHandler)

	http.HandleFunc("/save/", saveHandler)

	http.HandleFunc("/blue/", blueHandler)

	http.HandleFunc("/composite_map/", compositeMapHandler)

	http.HandleFunc("/composite_page/", compositePageHandler)

	http.HandleFunc("/", homeHandler)

	fmt.Println("Starting server on port 8080")

	log.Fatal(http.ListenAndServe(":8080", nil))

}
