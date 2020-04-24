package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"github.com/gregcusack/ec_deployer/structs"
	"github.com/Maziyar-Na/EC-Agent/msg"
)


//type DeploymentDefinition struct {
//	DCDefs []struct {
//		Name  string `json:"name"`
//		Specs struct {
//			Mem   int `json:"mem"`
//			CPU   int `json:"cpu"`
//			Ports int `json:"ports"`
//			Net   int `json:"net"`
//		} `json:"specs"`
//		GcmIP    string   `json:"gcmIP"`
//		AgentIPs []string `json:"agentIPs"`
//		Images   []string `json:"images"`
//		PodNames []string `json:"pod-names"`
//		YamlPath []string `json:"yaml_path"`
//	} `json:"DC-def"`
//}

func main() {

	args := os.Args[1:]

	if len(args) != 1 {
		log.Fatal("Pls pass in deploy file via: ./main .go <json-file>")
	}

	jsonFile, err := os.Open(args[0])
	if err != nil {
		log.Println(err)
	}
	defer jsonFile.Close()

	byteVal, _ := ioutil.ReadAll(jsonFile)

	//var dcDefs DcDefs
	var dcDefs structs.DeploymentDefinition

	//json.Unmarshal(byteVal, &dcDefs)
	json.Unmarshal(byteVal, &dcDefs)

	for i := 0; i < len(dcDefs.DCDefs); i++ {
		fmt.Println("App Name: " + dcDefs.DCDefs[i].Name)
		fmt.Println("Mem Specs: " + strconv.Itoa(dcDefs.DCDefs[i].Specs.Mem))
		fmt.Println("Cpu Specs: " + strconv.Itoa(dcDefs.DCDefs[i].Specs.CPU))
		fmt.Println("Pod Names: " + dcDefs.DCDefs[i].PodNames[i])
	}




}
