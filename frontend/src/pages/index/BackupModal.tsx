import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Checkbox, Input, Modal, Popconfirm, Switch } from 'antd';
import { DownloadOutlined, GithubOutlined, UploadOutlined } from '@ant-design/icons';

import { HttpUtil, PromiseUtil } from '@/utils';
import './BackupModal.css';

interface BusyEvent {
  busy: boolean;
  tip?: string;
}

interface BackupModalProps {
  open: boolean;
  basePath: string;
  onClose: () => void;
  onBusy: (e: BusyEvent) => void;
}

export default function BackupModal({
  open,
  basePath: _basePath,
  onClose,
  onBusy,
}: BackupModalProps) {
  const { t } = useTranslation();
  const isPostgres = window.X_UI_DB_TYPE === 'postgres';
  const [keepHostSettings, setKeepHostSettings] = useState(true);
  const [allSettings, setAllSettings] = useState<Record<string, unknown> | null>(null);
  const [ghHasToken, setGhHasToken] = useState(false);
  const [gh, setGh] = useState({
    ghBackupEnable: false,
    ghBackupToken: '',
    ghBackupRepo: '',
    ghBackupFile: '',
  });

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    (async () => {
      const msg = await HttpUtil.post('/panel/api/setting/all', undefined, { silent: true });
      const obj = (msg?.obj ?? {}) as Record<string, unknown>;
      if (cancelled) return;
      setAllSettings(obj);
      setGhHasToken(!!obj.hasGhBackupToken);
      setGh({
        ghBackupEnable: !!obj.ghBackupEnable,
        ghBackupToken: '',
        ghBackupRepo: typeof obj.ghBackupRepo === 'string' ? obj.ghBackupRepo : '',
        ghBackupFile: typeof obj.ghBackupFile === 'string' ? obj.ghBackupFile : '',
      });
    })();
    return () => {
      cancelled = true;
    };
  }, [open ]);

  async function saveGh() {
    if (!allSettings) return;
    const payload: Record<string, unknown> = { ...allSettings, ...gh };
    for (const key of Object.keys(payload)) {
      if (key.startsWith('has')) delete payload[key];
    }
    const msg = await HttpUtil.post('/panel/api/setting/update', payload);
    if (msg?.success && gh.ghBackupToken) setGhHasToken(true);
  }

  async function runGhBackup() {
    await HttpUtil.post('/panel/api/server/ghBackupRun');
  }

  async function importGh() {
    onClose();
    onBusy({ busy: true, tip: `${t('pages.index.ghBackupImport')}…` });
    const msg = await HttpUtil.post('/panel/api/server/ghBackupImport');
    if (!msg?.success) {
      onBusy({ busy: false });
      return;
    }
    onBusy({ busy: true, tip: `${t('pages.settings.restartPanel')}…` });
    await PromiseUtil.sleep(5000);
    window.location.reload();
  }

  function exportDb() {
    window.location.href = (window.X_UI_BASE_PATH || '') + 'panel/api/server/getDb';
  }

  function exportMigration() {
    window.location.href = (window.X_UI_BASE_PATH || '') + 'panel/api/server/getMigration';
  }

  function importDb() {
    const fileInput = document.createElement('input');
    fileInput.type = 'file';
    fileInput.accept = '.dump,.db';
    fileInput.addEventListener('change', async (e) => {
      const dbFile = (e.target as HTMLInputElement).files?.[0];
      if (!dbFile) return;

      const formData = new FormData();
      formData.append('db', dbFile);
      formData.append('keepHostSettings', String(keepHostSettings));

      onClose();
      onBusy({ busy: true, tip: `${t('pages.index.importDatabase')}…` });

      const upload = await HttpUtil.post('/panel/api/server/importDB', formData, {
        headers: { 'Content-Type': 'multipart/form-data' },
      });
      if (!upload?.success) {
        onBusy({ busy: false });
        return;
      }

      // importDB schedules the panel restart server-side; wait it out, then reload.
      onBusy({ busy: true, tip: `${t('pages.settings.restartPanel')}…` });
      await PromiseUtil.sleep(5000);
      window.location.reload();
    });
    fileInput.click();
  }

  return (
    <Modal open={open} title={t('pages.index.backupTitle')} footer={null} onCancel={onClose}>
      {isPostgres && (
        <div className="backup-description" style={{ marginBottom: 16 }}>
          {t('pages.index.backupPostgresNote')}
        </div>
      )}
      <div className="backup-list">
        <div className="backup-item">
          <div className="backup-meta">
            <div className="backup-title">{t('pages.index.exportDatabase')}</div>
            <div className="backup-description">
              {isPostgres
                ? t('pages.index.exportDatabasePgDesc')
                : t('pages.index.exportDatabaseDesc')}
            </div>
          </div>
          <Button
            type="primary"
            aria-label={t('pages.index.exportDatabase')}
            onClick={exportDb}
            icon={<DownloadOutlined aria-hidden />}
          />
        </div>

        {isPostgres && (
          <div className="backup-item">
            <div className="backup-meta">
              <div className="backup-title">{t('pages.index.migrationDownload')}</div>
              <div className="backup-description">{t('pages.index.migrationDownloadPgDesc')}</div>
            </div>
            <Button
              type="primary"
              aria-label={t('pages.index.migrationDownload')}
              onClick={exportMigration}
              icon={<DownloadOutlined aria-hidden />}
            />
          </div>
        )}

        <div className="backup-item">
          <div className="backup-meta">
            <div className="backup-title">{t('pages.index.importDatabase')}</div>
            <div className="backup-description">
              {isPostgres
                ? t('pages.index.importDatabasePgDesc')
                : t('pages.index.importDatabaseDesc')}
            </div>
          </div>
          <Button
            type="primary"
            aria-label={t('pages.index.importDatabase')}
            onClick={importDb}
            icon={<UploadOutlined aria-hidden />}
          />
        </div>

        <div className="backup-item">
          <div className="backup-meta">
            <Checkbox
              checked={keepHostSettings}
              onChange={(e) => setKeepHostSettings(e.target.checked)}
            >
              {t('pages.index.importKeepHostSettings')}
            </Checkbox>
            <div className="backup-description">{t('pages.index.importKeepHostSettingsDesc')}</div>
          </div>
        </div>

        <div className="backup-item">
          <div className="backup-meta">
            <div className="backup-title">
              <GithubOutlined aria-hidden /> {t('pages.index.ghBackup')}
            </div>
            <div className="backup-description">{t('pages.index.ghBackupDesc')}</div>
          </div>
          <Switch
            checked={gh.ghBackupEnable}
            onChange={(v) => setGh({ ...gh, ghBackupEnable: v })}
            aria-label={t('pages.index.ghBackupEnable')}
          />
        </div>

        <div className="backup-item">
          <div className="backup-meta" style={{ flex: 1 }}>
            <div className="backup-title">{t('pages.index.ghBackupToken')}</div>
            <Input.Password
              value={gh.ghBackupToken}
              onChange={(e) => setGh({ ...gh, ghBackupToken: e.target.value })}
              placeholder={ghHasToken ? '••••••••' : 'ghp_…'}
              autoComplete="off"
            />
          </div>
        </div>

        <div className="backup-item">
          <div className="backup-meta" style={{ flex: 1 }}>
            <div className="backup-title">{t('pages.index.ghBackupRepo')}</div>
            <Input
              value={gh.ghBackupRepo}
              onChange={(e) => setGh({ ...gh, ghBackupRepo: e.target.value })}
              placeholder="owner/repo"
            />
          </div>
        </div>

        <div className="backup-item">
          <div className="backup-meta" style={{ flex: 1 }}>
            <div className="backup-title">{t('pages.index.ghBackupFile')}</div>
            <Input
              value={gh.ghBackupFile}
              onChange={(e) => setGh({ ...gh, ghBackupFile: e.target.value })}
              placeholder={t('pages.index.ghBackupFileAuto')}
            />
          </div>
        </div>

        <div className="backup-item">
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            <Button onClick={saveGh}>{t('pages.index.ghBackupSave')}</Button>
            <Button type="primary" onClick={runGhBackup}>
              {t('pages.index.ghBackupRunNow')}
            </Button>
            <Popconfirm
              title={t('pages.index.ghBackupImportConfirm')}
              onConfirm={importGh}
              okText={t('pages.index.ghBackupImport')}
            >
              <Button danger>{t('pages.index.ghBackupImport')}</Button>
            </Popconfirm>
          </div>
        </div>
      </div>
    </Modal>
  );
}
