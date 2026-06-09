#!/system/bin/sh

bootfile="/tmp/stb_boot.pcap"
tmpdir="/tmp"

touch $bootfile
chmod 777 $bootfile
chown system:system $bootfile
size=$(getprop persist.sys.sw.dumpsize)

echo "size==${size}"
if [ -d  $tmpdir ];then
    echo "tcpdump begin..."
    if [ -z "${size}" ];then
        tcpdump -i any -p -s 0 -w $bootfile
    else
        #如果配置属性设置了大小，按照配置进行分包抓取，抓包结束后，apk需要把属性设置为空，防止影响下次使用
        tcpdump -i any -p -s 0 -C "${size}" -w $bootfile
    fi
else
    echo "tcpdump fail..."
fi
