package discovery

import "math"

// haversineKm is the great-circle distance; it matches ST_Distance_Sphere (mean radius
// 6,370,986 m) closely enough for coverage decisions in the daily evaluation.
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6370.986
	toRad := math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLng := (lng2 - lng1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}
