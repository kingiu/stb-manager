#!/system/bin/sh

authkey=`getprop ro.authkey`
mac=`getprop ro.mac`
sn=`getprop ro.serialno`
cmcc_sn=`getprop ro.serialno`
cmei=`getprop ro.cmei`
MAC=`echo $mac | busybox  sed 's/://g'`
memSize=`getprop ro.memory.size`
flashSize=`getprop ro.flash.size`
bluetoothMac=`cat /data/misc/bluedroid/bt_config.conf  | grep Address | cut -d ' ' -f 3`
bluetoothMacAddress=`echo $bluetoothMac | busybox  sed 's/://g'`
version=`getprop ro.build.version.incremental`

province=`getprop ro.sw.publish_region`
productModel=`getprop ro.build.devicemodel`
stbModel=`getprop ro.stb_model`
mkdir /private/devinfo
chown system:system /private/devinfo
chmod 777 /private/devinfo
#开机抓包服务
TCPDUMP_ON=`getprop persist.sys.tcpdump.capture`

#setprop "ro.bootmac" $mac

#ifdef CMDC_EDIT
#lizhiyuan@cmdc.stb, 230925, set igmp default version to v2.
echo 2 > /proc/sys/net/ipv4/conf/eth0/force_igmp_version
#endif /*CMDC_EDIT*/

if [ -n ${MAC} ];then
	setprop "ro.andlink.deviceMac" $MAC
	setprop "ro.andlink.mac" $MAC
fi
if [ -n ${sn} ];then
	setprop "ro.andlink.stbId" "${sn}"
	setprop "ro.andlink.authId" "${sn}"
	setprop "ro.andlink.sn" "${sn}"
	setprop "android.os.Build.SERIAL" "${sn}"
	setprop "ro.stbid" "${sn}"
	setprop "ro.boot.stbid" "${sn}"
	setprop "ro.product.stb.serialnum" "${sn}"
	setprop "ro.product.stb.stbid" "${sn}"
	setprop "ro.product.stb.tvid" "${sn}"
fi
if [ -n ${cmcc_sn} ];then
	setprop "ro.deviceid" "${cmcc_sn}"
fi
if [ -n ${cemi} ];then
	setprop "ro.andlink.cmei" "${cmei}"
else
	setprop "ro.andlink.cmei" "${cmcc_sn}"
fi
if [ -n ${authkey} ];then
	setprop "ro.andlink.authKey" "${authkey}"
fi
if [ -n ${memSize} ];then
	setprop "ro.andlink.romStorageSize" "${memSize}"
fi
if [ -n ${flashSize} ];then
	setprop "ro.andlink.ramStorageSize" "${flashSize}"
fi
if [ -n ${bluetoothMacAddress} ];then
	setprop "ro.andlink.bluetoothMacAddress" "${bluetoothMacAddress}"
fi


if [ "$TCPDUMP_ON" == "true"  ];then
	setprop ro.vendor.tcpdump.enabled true
else
	setprop ro.vendor.tcpdump.enabled false
fi

#浙江设置软件版本属性供应用商城获取
#ifdef CMDC_EDIT
#liujunhua@cmdc.stb, 230829, zhejiang add soft version
if [[ "cmcc_zj" == ${province} ]];then
    setprop "persist.sys.versioninfo" "${version}"
fi
#endif CMDC_EDIT

#U盘升级路径，多型号适配
if [[ "cmcc_hl" == ${province} ]];then
    setprop "sys.sw.upgrade.usbpath" "${productModel}"
elif [[ "cmcc_jx" == ${province} && -n ${productModel} ]];then
        if [[  ${productModel} == *-ZG ]];then
        setprop "sys.sw.upgrade.usbpath" "/CMDC/${productModel/-ZG/_ZG}"
        elif [[  ${productModel} == *-CH ]];then
        setprop "sys.sw.upgrade.usbpath" "/CMDC/${productModel/-CH/_CH}"
        elif [[  ${productModel} == *-YST ]];then
        setprop "sys.sw.upgrade.usbpath" "/CMDC/${productModel/-YST/_YST}"
        # added by qiujiejun@cmdc, 2023-05-12, handle "YS" suffix in model name
        elif [[  ${productModel} == *-YS ]];then
            setprop "sys.sw.upgrade.usbpath" "/CMDC/${productModel/-YS/_YS}"
        elif [[  ${productModel} == *-HV ]];then
            # add by ruqnwenjiang, 2023-11-20, handle huawei suffix in model name.
            setprop "sys.sw.upgrade.usbpath" "/CMDC/${productModel/-HV/_HV}"
        fi
elif [[ "cmcc_ha" == ${province} ]];then
        echo 3 > /proc/sys/net/ipv4/conf/eth0/force_igmp_version
elif [[ "cmcc_sd" == ${province} ]];then
      rm /data/misc/dhcp/dhclient6_eth0.leases
      setprop "persist.dhcp.lease.cAddr.eth0" ""
      setprop "persist.dhcp.lease.sAddr.eth0" ""
elif [[ "cmcc_hb" == ${province} ]];then
     setprop "sys.sw.upgrade.usbpath" "/" 
fi


#老化文件权限
chmod 777 /cache/burntest
chmod 777 /cache/burntest/*
if [ -f /cache/H264.ts ];then
    mv /cache/H264.ts /data/H264.ts
fi
if [ -d /cache/burntest ];then
    chmod 755 /cache/content.properties
    chmod 755 /cache/extend_files.csv
    chmod 755 /data/H264.ts
    chmod 755 /cache/OTT
    chmod 755 /cache/parameters.ini
    chmod 755 /cache/SWProductTest.apk
fi

if [[ -f "/cache/update/update.zip" ]];then
	rm /cache/update/update.zip 
fi
if [[ -f /cache/upgrade/upgrade.zip ]];then
	rm /cache/upgrade/upgrade.zip 
fi
if [[ -f /cache/upgrade/update.zip ]];then
	rm /cache/upgrade/update.zip 
fi
if [[ -f "/cache/recovery/update.zip" ]];then
	rm /cache/recovery/update.zip 
fi
#ifdef CMDC_EDIT
#liujunhua@cmdc.stb,20220609,Delete update package after update.
if [[ -f "/cache/update.zip" ]];then
	rm /cache/update.zip
fi
if [[ -f "/cache/upgrade.zip" ]];then
	rm /cache/upgrade.zip
fi
if [[ -f "/cache/tr069_update.zip" ]];then
	rm /cache/tr069_update.zip
fi
#endif
#daijingyu@cmdc.stb,20230330,for goke dolby plus lib copy.
if [[ ${stbModel} == "CH" ]];then
    if [[ -f "/vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_ch.so" ]];then
        mount -o remount,rw /vendor
        cp /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_ch.so /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode.so
        rm /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_*
    fi
elif [[ ${stbModel} == "YS" || ${stbModel} == "YST" ]];then
    if [[ -f "/vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_ys.so" ]];then
        mount -o remount,rw /vendor
        cp /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_ys.so /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode.so
        rm /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_*
    fi
elif [[ ${stbModel} == "ZG" ]];then
    if [[ -f "/vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_zg.so" ]];then
        mount -o remount,rw /vendor
        cp /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_zg.so /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode.so
        rm /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_*
    fi
elif [[ ! -f "/vendor/lib/libHA.AUDIO.DOLBYPLUS.decode.so" ]];then
    if [[ -f "/vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_zg.so" ]];then
        mount -o remount,rw /vendor
        cp /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode_zg.so /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode.so
        #add by daijingyu.20240129,for SM&NL delete the dolby library,need to update their own library.
        if [[ ${stbModel} == "SM" || ${stbModel} == "NL" ]];then
            rm -f /vendor/lib/libHA.AUDIO.DOLBYPLUS.decode.so
        fi
        #end
    fi
fi
