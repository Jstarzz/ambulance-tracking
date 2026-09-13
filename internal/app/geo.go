package app

import "math"

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusM = 6_371_000.0
	toRad := math.Pi / 180
	phi1 := lat1 * toRad
	phi2 := lat2 * toRad
	dPhi := (lat2 - lat1) * toRad
	dLambda := (lon2 - lon1) * toRad
	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) + math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	return 2 * earthRadiusM * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
