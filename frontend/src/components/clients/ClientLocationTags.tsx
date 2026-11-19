import { Tag } from 'antd';

interface ClientLocationTagsProps {
  labels: string[];
}

// Location tags naming the config(s) a client is currently connected
// through — one per hosting location (node name, or this panel's own
// inbound remark for local connections). Renders nothing when the client
// is not connected anywhere.
export default function ClientLocationTags({ labels }: ClientLocationTagsProps) {
  if (!labels.length) return null;
  return (
    <div
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        gap: 4,
        justifyContent: 'center',
      }}
    >
      {labels.map((label) => (
        <Tag key={label} color="geekblue" style={{ margin: 0 }}>
          {label}
        </Tag>
      ))}
    </div>
  );
}
