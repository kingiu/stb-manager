#!/system/bin/sh

binFolder=/system/bin
dataFolder=/data/data/com.vixtel.stb.agent

testAgentFileName=testagent
stbAgentFileName=stbagent

testAgentFullFolder=$(echo "$binFolder/$testAgentFileName")
stbAgentFullFolder="$dataFolder/$stbAgentFileName"

testagentDataFile="$dataFolder/$testAgentFileName"

echo $testAgentFullFolder

sleep 2


while [ ! -f $testAgentFullFolder ]
do
	sleep 1;
done


if [ -f $testagentDataFile ];then
    echo "($testagentDataFile) exist!"
	$(chmod 755 $testagentDataFile)
    $testagentDataFile >/dev/null 2>&1 &
else
    $testAgentFullFolder >/dev/null 2>&1 &
fi

sleep 2

if [ -f $stbAgentFullFolder ] ; then
	echo "($stbAgentFullFolder) exist!"
	$(chmod 755 $stbAgentFullFolder)
	$stbAgentFullFolder >/dev/null 2>&1 &
	echo "vxt_true"
else  
	echo "vxt_false"
fi

