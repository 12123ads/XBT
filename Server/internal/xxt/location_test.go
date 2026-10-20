package xxt

import (
	"math"
	"math/rand/v2"
	"testing"
)

func assertSixDecimals(t *testing.T, value float64) {
	t.Helper()
	scaled := value * 1e6
	if math.Abs(scaled-math.Round(scaled)) > 1e-6 {
		t.Fatalf("coordinate %v is not rounded to six decimals", value)
	}
}

func TestSampleLocationStaysWithinRadius(t *testing.T) {
	cases := []struct {
		name   string
		center GeoPoint
		radius float64
	}{
		{name: "日照 200m", center: GeoPoint{Latitude: 35.47574, Longitude: 119.535984}, radius: 200},
		{name: "上海 1500m", center: GeoPoint{Latitude: 31.2304, Longitude: 121.4737}, radius: 1500},
		{name: "高纬 100m", center: GeoPoint{Latitude: 63.5, Longitude: 10.4}, radius: 100},
		{name: "赤道 12m", center: GeoPoint{Latitude: 0.00001, Longitude: 0.00001}, radius: 12},
		{name: "极小半径 1m", center: GeoPoint{Latitude: 30.123456, Longitude: 120.987654}, radius: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewPCG(0x5eed, uint64(tc.radius)))
			const samples = 10000
			for i := 0; i < samples; i++ {
				position := SampleLocation(tc.center, tc.radius, rng)
				assertSixDecimals(t, position.Latitude)
				assertSixDecimals(t, position.Longitude)
				point := GeoPoint{Latitude: position.Latitude, Longitude: position.Longitude}
				if distance := DistanceMeters(point, tc.center); distance > tc.radius {
					t.Fatalf("sample %d distance %.3fm exceeds radius %.0fm: %+v", i, distance, tc.radius, point)
				}
			}
		})
	}
}

func TestSampleLocationJitterStaysWithinRadiusAndOffCenter(t *testing.T) {
	center := GeoPoint{Latitude: 35.475740, Longitude: 119.535984}
	const radius = 5.0
	rng := rand.New(rand.NewPCG(0x5eed, 26))
	for i := 0; i < 10000; i++ {
		position := SampleLocationJitter(center, radius, rng)
		assertSixDecimals(t, position.Latitude)
		assertSixDecimals(t, position.Longitude)
		distance := DistanceMeters(GeoPoint{Latitude: position.Latitude, Longitude: position.Longitude}, center)
		if distance <= 0 || distance > radius {
			t.Fatalf("sample %d distance %.4fm outside (0, %.0f]: %+v", i, distance, radius, position)
		}
	}
}

func TestDistanceMetersKnownOffsets(t *testing.T) {
	oneDegreeLat := DistanceMeters(GeoPoint{Latitude: 0, Longitude: 0}, GeoPoint{Latitude: 0.001, Longitude: 0})
	if math.Abs(oneDegreeLat-111.195) > 0.2 {
		t.Fatalf("0.001 degree latitude = %.3fm, want about 111.2m", oneDegreeLat)
	}
	atSixty := DistanceMeters(GeoPoint{Latitude: 60, Longitude: 0}, GeoPoint{Latitude: 60, Longitude: 0.001})
	if math.Abs(atSixty-55.6) > 0.2 {
		t.Fatalf("0.001 degree longitude at 60N = %.3fm, want about 55.6m", atSixty)
	}
	if got := DistanceMeters(GeoPoint{Latitude: 30, Longitude: 120}, GeoPoint{Latitude: 30, Longitude: 120}); got != 0 {
		t.Fatalf("same point distance = %v, want 0", got)
	}
}

func TestPositionWithinRange(t *testing.T) {
	center := GeoPoint{Latitude: 30, Longitude: 120}
	inside := GeoPoint{Latitude: 30.0001, Longitude: 120}
	distance, ok := PositionWithinRange(inside, center, 50)
	if !ok || distance <= 0 || distance > 50 {
		t.Fatalf("inside point distance=%.3f ok=%v", distance, ok)
	}
	outside := GeoPoint{Latitude: 30.001, Longitude: 120}
	distance, ok = PositionWithinRange(outside, center, 50)
	if ok || distance <= 50 {
		t.Fatalf("outside point distance=%.3f ok=%v, want rejected", distance, ok)
	}
	if _, ok := PositionWithinRange(outside, center, 0); !ok {
		t.Fatal("non-positive radius must not reject a point without metadata")
	}
}

func TestPositionFromSpecialAcceptsStringAndNumber(t *testing.T) {
	fromStrings := PositionFromSpecial(map[string]interface{}{
		"latitude": "30.1234567", "longitude": "120.7654321", "description": " 教学楼 ",
	})
	if fromStrings == nil || fromStrings.Latitude != 30.1234567 || fromStrings.Longitude != 120.7654321 || fromStrings.Address != "教学楼" {
		t.Fatalf("string position = %+v", fromStrings)
	}
	fromNumbers := PositionFromSpecial(map[string]interface{}{"latitude": 30.5, "longitude": 120.5})
	if fromNumbers == nil || fromNumbers.Latitude != 30.5 || fromNumbers.Longitude != 120.5 {
		t.Fatalf("numeric position = %+v", fromNumbers)
	}
	for _, special := range []map[string]interface{}{
		{"latitude": "30.1"},
		{"latitude": "abc", "longitude": "120"},
		{"latitude": "NaN", "longitude": "120"},
		{"latitude": "Inf", "longitude": "120"},
		{"latitude": "91", "longitude": "120"},
		{"latitude": "30", "longitude": "181"},
	} {
		if got := PositionFromSpecial(special); got != nil {
			t.Fatalf("partial/invalid position %v parsed as %+v", special, got)
		}
	}
}
