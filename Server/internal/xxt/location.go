package xxt

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
)

// earthRadiusMeters is the mean Earth radius used by the haversine distance.
const earthRadiusMeters = 6371008.8

// GeoPoint is a WGS/BD-09 agnostic latitude/longitude pair. The protocol only
// compares points expressed in the same coordinate system, which both the
// activity metadata and the web picker provide as BD-09.
type GeoPoint struct {
	Latitude  float64
	Longitude float64
}

// ValidGeoPoint reports whether a pair can be used as a location center.
// (0,0) is treated as missing: it is what the metadata endpoint returns when
// the teacher never set a point, and submitting it produces a ~12,000 km
// distance error upstream (XBT-S19).
func ValidGeoPoint(point GeoPoint) bool {
	if point.Latitude == 0 && point.Longitude == 0 {
		return false
	}
	return point.Latitude >= -90 && point.Latitude <= 90 &&
		point.Longitude >= -180 && point.Longitude <= 180
}

// DistanceMeters returns the great-circle distance between two points. It is
// accurate enough for the tens-to-hundreds-of-metres activity ranges and keeps
// the server-side check independent of the map provider.
func DistanceMeters(a, b GeoPoint) float64 {
	lat1 := a.Latitude * math.Pi / 180
	lat2 := b.Latitude * math.Pi / 180
	dLat := lat2 - lat1
	dLng := (b.Longitude - a.Longitude) * math.Pi / 180

	sinLat := math.Sin(dLat / 2)
	sinLng := math.Sin(dLng / 2)
	h := sinLat*sinLat + math.Cos(lat1)*math.Cos(lat2)*sinLng*sinLng
	if h > 1 {
		h = 1
	}
	return 2 * earthRadiusMeters * math.Asin(math.Sqrt(h))
}

// PositionWithinRange reports the distance from center to pos and whether the
// point is inside radiusMeters. A non-positive radius cannot constrain a point,
// so callers must treat only radius > 0 as authoritative metadata.
func PositionWithinRange(pos, center GeoPoint, radiusMeters float64) (float64, bool) {
	distance := DistanceMeters(pos, center)
	if radiusMeters <= 0 {
		return distance, true
	}
	return distance, distance <= radiusMeters
}

// SampleLocation returns an area-uniform random point inside the circle defined
// by center/radiusMeters, rounded to six decimals. Rounding can move a point a
// few centimetres outwards, so the result is pulled back towards the center
// until DistanceMeters confirms it is inside. A nil rng uses the global,
// concurrency-safe source.
func SampleLocation(center GeoPoint, radiusMeters float64, rng *rand.Rand) SignPosition {
	if radiusMeters <= 0 {
		return SignPosition{Latitude: roundCoordinate(center.Latitude), Longitude: roundCoordinate(center.Longitude)}
	}
	uniform := func() float64 {
		if rng != nil {
			return rng.Float64()
		}
		return rand.Float64()
	}

	// Area-uniform disk sampling: r = R*sqrt(u) avoids clustering at the center.
	offset := radiusMeters * math.Sqrt(uniform())
	angle := 2 * math.Pi * uniform()
	latOffset := offset * math.Cos(angle) / metersPerDegreeLatitude
	cosLat := math.Cos(center.Latitude * math.Pi / 180)
	if math.Abs(cosLat) < 1e-6 {
		cosLat = 1e-6
	}
	lngOffset := offset * math.Sin(angle) / (metersPerDegreeLatitude * cosLat)

	point := GeoPoint{
		Latitude:  roundCoordinate(center.Latitude + latOffset),
		Longitude: roundCoordinate(center.Longitude + lngOffset),
	}
	for attempt := 0; attempt < 4 && DistanceMeters(point, center) > radiusMeters; attempt++ {
		point.Latitude = roundCoordinate(center.Latitude + (point.Latitude-center.Latitude)*0.999)
		point.Longitude = roundCoordinate(center.Longitude + (point.Longitude-center.Longitude)*0.999)
	}
	if DistanceMeters(point, center) > radiusMeters {
		point = GeoPoint{Latitude: roundCoordinate(center.Latitude), Longitude: roundCoordinate(center.Longitude)}
	}
	return SignPosition{Latitude: point.Latitude, Longitude: point.Longitude}
}

// SampleLocationJitter returns an area-uniform random point within
// radiusMeters of center, rounded to six decimals. Unlike SampleLocation it
// never returns center itself: a zero offset would submit the selected point
// verbatim, which is the fingerprint the per-sign jitter exists to avoid
// (XBT-S26). The result satisfies 0 < DistanceMeters(point, center) <=
// radiusMeters whenever radiusMeters exceeds the six-decimal resolution
// (~0.11m). Below that no representable offset is both nonzero and inside the
// circle, so the rounded center is returned.
func SampleLocationJitter(center GeoPoint, radiusMeters float64, rng *rand.Rand) SignPosition {
	if radiusMeters <= 0 {
		return SignPosition{Latitude: roundCoordinate(center.Latitude), Longitude: roundCoordinate(center.Longitude)}
	}
	for attempt := 0; attempt < 8; attempt++ {
		position := SampleLocation(center, radiusMeters, rng)
		point := GeoPoint{Latitude: position.Latitude, Longitude: position.Longitude}
		if distance := DistanceMeters(point, center); distance > 0 && distance <= radiusMeters {
			return position
		}
	}
	// Deterministic fallback: half the radius due north. Rounding keeps it
	// inside; a radius below the coordinate resolution yields center.
	point := GeoPoint{
		Latitude:  roundCoordinate(center.Latitude + radiusMeters/2/metersPerDegreeLatitude),
		Longitude: roundCoordinate(center.Longitude),
	}
	if distance := DistanceMeters(point, center); distance > 0 && distance <= radiusMeters {
		return SignPosition{Latitude: point.Latitude, Longitude: point.Longitude}
	}
	return SignPosition{Latitude: roundCoordinate(center.Latitude), Longitude: roundCoordinate(center.Longitude)}
}

// metersPerDegreeLatitude is the mean meridional length of one degree.
const metersPerDegreeLatitude = 111132.92

// PositionFromSpecial reads the caller-selected point from the API payload:
// {latitude, longitude, description}. Values may be strings (web) or numbers
// (share/other clients). Both coordinates are required; partial input is dropped.
func PositionFromSpecial(special map[string]interface{}) *SignPosition {
	latStr := strings.TrimSpace(strVal(special["latitude"]))
	lngStr := strings.TrimSpace(strVal(special["longitude"]))
	if latStr == "" || lngStr == "" {
		return nil
	}
	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		return nil
	}
	lng, err := strconv.ParseFloat(lngStr, 64)
	if err != nil {
		return nil
	}
	if math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) {
		return nil
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return nil
	}
	return &SignPosition{
		Latitude:  lat,
		Longitude: lng,
		Address:   strings.TrimSpace(strVal(special["description"])),
	}
}
