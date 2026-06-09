#!/system/bin/sh
if ! applypatch -c EMMC:/dev/block/platform/soc/f9830000.gkmciv200.MMC/by-name/recovery:13926400:3590e8ce537ab9e3fda3bb2eebae95bd548785e9; then
  applypatch  EMMC:/dev/block/platform/soc/f9830000.gkmciv200.MMC/by-name/boot:11550720:f3364d11179f6503643e9ab3ea9a1139437bd9d6 EMMC:/dev/block/platform/soc/f9830000.gkmciv200.MMC/by-name/recovery 3590e8ce537ab9e3fda3bb2eebae95bd548785e9 13926400 f3364d11179f6503643e9ab3ea9a1139437bd9d6:/system/recovery-from-boot.p && log -t recovery "Installing new recovery image: succeeded" || log -t recovery "Installing new recovery image: failed"
else
  log -t recovery "Recovery image already installed"
fi
