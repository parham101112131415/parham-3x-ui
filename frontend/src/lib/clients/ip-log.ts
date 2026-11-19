// Shape of one entry in a client's IP log, as returned by
// POST /panel/api/clients/ips/:email. `node` is the name of the node the IP is
// connecting through, or '' when it is on this local panel (or unattributed).
// `geo` is the IP's approximate (city-level) location when the panel could
// resolve one — coordinates included so IPs can be compared by place.
export type IpGeoInfo = {
  lat: number;
  lon: number;
  city: string;
  country: string;
};

export type ClientIpInfo = {
  ip: string;
  time: string;
  node: string;
  geo?: IpGeoInfo | null;
};

function normalizeGeo(v: unknown): IpGeoInfo | null {
  if (!v || typeof v !== 'object') return null;
  const o = v as Record<string, unknown>;
  if (typeof o.lat !== 'number' || typeof o.lon !== 'number') return null;
  return {
    lat: o.lat,
    lon: o.lon,
    city: typeof o.city === 'string' ? o.city : '',
    country: typeof o.country === 'string' ? o.country : '',
  };
}

// geoLabel renders one IP location as "City, Country (lat, lon)".
export function geoLabel(geo: IpGeoInfo): string {
  const place = [geo.city, geo.country].filter((s) => s && s.length > 0).join(', ');
  const coords = `${geo.lat.toFixed(4)}, ${geo.lon.toFixed(4)}`;
  return place ? `${place} (${coords})` : coords;
}

// geoMapsUrl opens the IP's coordinates in Google Maps.
export function geoMapsUrl(geo: IpGeoInfo): string {
  return `https://www.google.com/maps?q=${geo.lat},${geo.lon}`;
}

// normalizeClientIps accepts the API payload and returns typed entries. It also
// tolerates the legacy shape (a plain array of "ip (time)" strings) so the UI
// keeps working against older panels.
export function normalizeClientIps(obj: unknown): ClientIpInfo[] {
  if (!Array.isArray(obj)) return [];
  const out: ClientIpInfo[] = [];
  for (const x of obj) {
    if (typeof x === 'string') {
      if (x.length > 0) out.push({ ip: x, time: '', node: '', geo: null });
      continue;
    }
    if (x && typeof x === 'object') {
      const o = x as Record<string, unknown>;
      const ip = typeof o.ip === 'string' ? o.ip : '';
      if (!ip) continue;
      out.push({
        ip,
        time: typeof o.time === 'string' ? o.time : '',
        node: typeof o.node === 'string' ? o.node : '',
        geo: normalizeGeo(o.geo),
      });
    }
  }
  return out;
}
