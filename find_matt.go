package main

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"strconv"
)

type ColorValue interface {
	int | uint32
}

func largest[T ColorValue](a T, b T, c T) T {
	if a > b && a > c {
		return a
	} else if b > c {
		return b
	} else {
		return c
	}
}

func smallest[T ColorValue](a T, b T, c T) T {
	if a < b && a < c {
		return a
	} else if b < c {
		return b
	} else {
		return c
	}
}

type MyCol struct {
	Red   uint64 `json:"red"`
	Green uint64 `json:"green"`
	Blue  uint64 `json:"blue"`
}

func (col MyCol) Add(other MyCol) MyCol {
	col.Red += other.Red
	col.Green += other.Green
	col.Blue += other.Blue
	return col

}

func (col MyCol) Complement() MyCol {
	return MyCol{(65535 - col.Red) >> 2, (65535 - col.Green) >> 2, (65535 - col.Blue) >> 2}
}

func (col MyCol) Divide(count int) MyCol {
	col.Red = uint64(float32(col.Red) / float32(count))
	col.Green = uint64(float32(col.Green) / float32(count))
	col.Blue = uint64(float32(col.Blue) / float32(count))
	return col
}

func (col MyCol) GetRGBA() color.RGBA {
	return color.RGBA{uint8(col.Red >> 8), uint8(col.Green >> 8), uint8(col.Blue >> 8), 255}
}

func (col MyCol) Commaed() string {
	return "(" + Format(int64(col.Red)) + " ; " + Format(int64(col.Green)) + " ; " + Format(int64(col.Blue)) + ")"
}

func Format(n int64) string {
	in := strconv.FormatInt(n, 10)
	numOfDigits := len(in)
	if n < 0 {
		numOfDigits-- // First character is the - sign (not a digit)
	}
	numOfCommas := (numOfDigits - 1) / 3

	out := make([]byte, len(in)+numOfCommas)
	if n < 0 {
		in, out[0] = in[1:], '-'
	}

	for i, j, k := len(in)-1, len(out)-1, 0; ; i, j = i-1, j-1 {
		out[j] = in[i]
		if i == 0 {
			return string(out)
		}
		if k++; k == 3 {
			j, k = j-1, 0
			out[j] = ','
		}
	}
}

func get_matt_color(filename string) (MyCol, error) {

	file, err := os.Open(filename)
	if err != nil {
		fmt.Println("***** Error opening file:", err)
		return MyCol{}, err
	}
	defer file.Close()

	// Decode the image
	img, format, err := image.Decode(file)
	fmt.Println(format)
	if err != nil {
		fmt.Println("***** Error decoding image:", err)
		return MyCol{}, err
	}

	bounds := img.Bounds()
	fmt.Println(bounds)

	peak_saturation := 0

	saturation_buckets := make([]int, 64) // 0 is zero

	// Iterate over every pixel

	var max_brightness_seen uint32 = 0

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			// Get the color of the pixel
			r, g, b, _ := img.At(x, y).RGBA()
			max_brightness := largest(r, g, b)
			min_brightness := smallest(r, g, b)
			saturation := max_brightness - min_brightness
			approx_sat := saturation >> 10 // reduce from 16 bits to 6 bits

			saturation_buckets[approx_sat] += 1
			if max_brightness > max_brightness_seen {
				max_brightness_seen = max_brightness
			}

			// if x < 3 {
			// 	fmt.Printf("(%d,%d,%d) ", r, g, b)
			// }
			// fmt.Print(r)

			if saturation > uint32(peak_saturation) {
				peak_saturation = int(saturation)
			}
		}
	}

	target_proportion := 0.024

	target_count := int(target_proportion * float64(bounds.Dx()) * float64(bounds.Dy()))

	min_bucket_to_count := len(saturation_buckets)
	cumm_count := 0
	found := false
	for i := len(saturation_buckets) - 1; i > -1; i-- {
		fmt.Printf("\t%d: %d\n", i, saturation_buckets[i])
		cumm_count += saturation_buckets[i]
		if !found && cumm_count >= target_count {
			min_bucket_to_count = i
			found = true
		}

	}

	fmt.Printf("\n\n\tPeak saturation: %d\n", peak_saturation)
	fmt.Printf("\n\n\tMax brightness seen: %d\n", max_brightness_seen)
	fmt.Printf("\n\n\tTop bucket to include: %d\n", min_bucket_to_count)

	threshold := uint32(min_bucket_to_count << 10)

	// buckets:=[]

	fmt.Printf("\tThreshold: %d\n", threshold)

	newImg := image.NewRGBA(img.Bounds())

	counts_by_color := make([]int, 8) // red=msb=4, green=2, blue=1
	// total := MyCol{0, 0, 0}
	total_color_vals := make([]MyCol, 8)
	for i := 0; i < 8; i++ {
		total_color_vals[i] = MyCol{0, 0, 0}
	}

	// Iterate over every pixel
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			// Get the color of the pixel
			r, g, b, _ := img.At(x, y).RGBA()
			max_brightness := largest(r, g, b)
			min_brightness := smallest(r, g, b)
			saturation := max_brightness - min_brightness
			if saturation > threshold {
				// Find the most significant bits from the color
				new_r := (uint8)(r >> 15) // 0/1 << 7 // mapping 0-65535 -> 0 or 128
				new_g := (uint8)(g >> 15) // 0/1 << 7
				new_b := (uint8)(b >> 15) // 0/1 << 7
				three_bit_color := new_r<<2 + new_g<<1 + new_b
				if y < 2 {
					fmt.Print(three_bit_color, " ")
				}

				counts_by_color[three_bit_color]++
				total_color_vals[three_bit_color] = total_color_vals[three_bit_color].Add(MyCol{uint64(r), uint64(g), uint64(b)})
				// newImg.SetRGBA(x, y, color.RGBA{new_r << 7, new_g << 7, new_b << 7, 255})

			} else {
				// newImg.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
			}
			newImg.SetRGBA(x, y, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255})
		}
	}

	commonest_sat_color_threebit := -1
	frequency_of_commonest_sat_color := 0

	fmt.Println("\n\n\tFound commonest+saturated stuff:")
	for i := 1; i < 7; i++ {
		if counts_by_color[i] > frequency_of_commonest_sat_color {
			frequency_of_commonest_sat_color = counts_by_color[i]
			commonest_sat_color_threebit = i
		}

		fmt.Printf("\t\tColor: %d, Count: %d, Total: %s\n", i, counts_by_color[i], total_color_vals[i].Commaed())
	}
	fmt.Printf("\n\tCommonest of the sat colors roughly: %d\n", commonest_sat_color_threebit)

	commonest_sat_color := total_color_vals[commonest_sat_color_threebit].Divide(counts_by_color[commonest_sat_color_threebit])

	fmt.Printf("\n\tCommonest saturated color (48 bit): %d\n", commonest_sat_color)

	complement := commonest_sat_color.Complement()
	fmt.Printf("\n\tComplement of commonest saturated color (48 bit): %d\n", complement)

	// Draw a block of pixels in the top left with the commonest saturated color
	for y := 0; y < 80; y++ {
		for x := 0; x < 80; x++ {
			newImg.SetRGBA(x, y, commonest_sat_color.GetRGBA())
		}
	}

	// Draw a big block down the right hand side of the complementary color
	for y := 0; y < bounds.Dy(); y++ {
		for x := bounds.Dx() - 240; x < bounds.Dx(); x++ {
			newImg.SetRGBA(x, y, complement.GetRGBA())
		}
	}

	// Display the image
	file, err = os.Create("result.jpg")
	if err != nil {
		fmt.Println("Error creating file:", err)
		return MyCol{}, err
	}
	defer file.Close()
	err = jpeg.Encode(file, newImg, nil)
	if err != nil {
		fmt.Println("Error writing out image:", err)
		return MyCol{}, err
	}
	return complement, nil
}
