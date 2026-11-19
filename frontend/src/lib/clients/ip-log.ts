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
  // MaxMind accuracy radius in km, when known: the IP is somewhere within
  // this distance of the coordinates, not exactly at the pin.
  radiusKm?: number;
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
    radiusKm: typeof o.radiusKm === 'number' && o.radiusKm > 0 ? o.radiusKm : undefined,
  };
}

// geoLabel renders one IP location as "City, Country (lat, lon)", with a
// "±N km" suffix when the accuracy radius is known.
export function geoLabel(geo: IpGeoInfo): string {
  const place = [geo.city, geo.country].filter((s) => s && s.length > 0).join(', ');
  const coords = `${geo.lat.toFixed(4)}, ${geo.lon.toFixed(4)}`;
  const base = place ? `${place} (${coords})` : coords;
  return geo.radiusKm ? `${base} ±${geo.radiusKm} km` : base;
}

// geoMapsUrl opens the IP's coordinates in Google Maps.
export function geoMapsUrl(geo: IpGeoInfo): string {
  return `https://www.google.com/maps?q=${geo.lat},${geo.lon}`;
}

// One destination host a client connected to, as returned by
// POST /panel/api/clients/sites/:email — aggregated from the Xray access
// log across all nodes. `count` is accepted connections, `lastSeen` the
// most recent one. There is no per-site byte count: Xray accounts traffic
// per user in total, not per destination.
export type ClientSiteInfo = {
  host: string;
  service: string;
  count: number;
  lastSeen: string;
};

export function normalizeClientSites(obj: unknown): ClientSiteInfo[] {
  if (!Array.isArray(obj)) return [];
  const out: ClientSiteInfo[] = [];
  for (const x of obj) {
    if (!x || typeof x !== 'object') continue;
    const o = x as Record<string, unknown>;
    if (typeof o.host !== 'string' || !o.host) continue;
    out.push({
      host: o.host,
      service: typeof o.service === 'string' ? o.service : '',
      count: typeof o.count === 'number' ? o.count : 0,
      lastSeen: typeof o.lastSeen === 'string' ? o.lastSeen : '',
    });
  }
  return out;
}

export type ClientSubVisitInfo = {
  ip: string;
  device: string;
  outcome: string; // opened | granted | denied
  time: string;
};

export function normalizeClientSubVisits(obj: unknown): ClientSubVisitInfo[] {
  if (!Array.isArray(obj)) return [];
  const out: ClientSubVisitInfo[] = [];
  for (const x of obj) {
    if (!x || typeof x !== 'object') continue;
    const o = x as Record<string, unknown>;
    out.push({
      ip: typeof o.ip === 'string' ? o.ip : '',
      device: typeof o.device === 'string' ? o.device : '',
      outcome: typeof o.outcome === 'string' ? o.outcome : '',
      time: typeof o.time === 'string' ? o.time : '',
    });
  }
  return out;
}

// One browser-geolocation fix the subscriber's own browser sent from the
// /loc/<subId> check page, as returned by POST /panel/api/clients/gps/:email.
// Unlike IpGeoInfo (an ISP area), this is the device's own position at the
// moment the page was opened — point-in-time only.
export type ClientGpsInfo = {
  lat: number;
  lon: number;
  accuracyM: number;
  ip: string;
  time: string;
};

export function normalizeClientGps(obj: unknown): ClientGpsInfo[] {
  if (!Array.isArray(obj)) return [];
  const out: ClientGpsInfo[] = [];
  for (const x of obj) {
    if (!x || typeof x !== 'object') continue;
    const o = x as Record<string, unknown>;
    if (typeof o.lat !== 'number' || typeof o.lon !== 'number') continue;
    out.push({
      lat: o.lat,
      lon: o.lon,
      accuracyM: typeof o.accuracyM === 'number' ? o.accuracyM : 0,
      ip: typeof o.ip === 'string' ? o.ip : '',
      time: typeof o.time === 'string' ? o.time : '',
    });
  }
  return out;
}

// gpsLabel renders one fix as "lat, lon (±N m)".
export function gpsLabel(gps: ClientGpsInfo): string {
  const coords = `${gps.lat.toFixed(5)}, ${gps.lon.toFixed(5)}`;
  return gps.accuracyM > 0 ? `${coords} (±${Math.round(gps.accuracyM)} m)` : coords;
}

export function gpsMapsUrl(gps: ClientGpsInfo): string {
  return `https://www.google.com/maps?q=${gps.lat},${gps.lon}`;
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
