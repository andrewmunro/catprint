// Phase 2 visual check: render two example markdown docs to PNG so we can
// eyeball font sizes and spacing before printing on paper.
package main

import (
	"fmt"
	"image/png"
	"log"
	"os"
	"time"

	"github.com/synestry/catprint/render"
	"github.com/synestry/catprint/validate"
)

const grocery = `# Shopping

- [ ] Apples
- [ ] Milk
- [x] Eggs
- [ ] Sourdough loaf
- [ ] **Dark** chocolate

---

Don't forget the reusable bag.`

const itinerary = `# Trip 5 Jun

## Morning

- 09:00 Train from Paddington
- 10:42 Arrive Bath Spa
- 11:00 Roman Baths tour

## Afternoon

- 13:30 Lunch at Sally Lunn's
- 15:00 Walk the Crescent

---

Return train 18:15. Bring **passport** and umbrella.`

func main() {
	docs := map[string]string{
		"test_grocery.png":   grocery,
		"test_itinerary.png": itinerary,
	}
	chrome := render.Chrome{Now: time.Date(2026, 5, 31, 7, 32, 0, 0, time.Local), Source: "web"}

	for name, md := range docs {
		if r := validate.Validate(md); !r.OK() {
			log.Printf("%s: validation: %+v", name, r.Violations)
		}
		img, err := render.RenderMarkdown(md, chrome)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s render: %v\n", name, err)
			os.Exit(1)
		}
		f, err := os.Create(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s create: %v\n", name, err)
			os.Exit(1)
		}
		if err := png.Encode(f, img); err != nil {
			fmt.Fprintf(os.Stderr, "%s encode: %v\n", name, err)
			os.Exit(1)
		}
		_ = f.Close()
		log.Printf("wrote %s (%dx%d)", name, img.Bounds().Dx(), img.Bounds().Dy())
	}
}
